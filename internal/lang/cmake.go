package lang

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// cmakePreset is the subset of a CMake preset the command resolver needs. The
// same shape serves configure, build and test presets.
type cmakePreset struct {
	Name            string                     `json:"name"`
	Hidden          bool                       `json:"hidden"`
	Inherits        json.RawMessage            `json:"inherits"`
	BinaryDir       string                     `json:"binaryDir"`
	ConfigurePreset string                     `json:"configurePreset"`
	CacheVariables  map[string]json.RawMessage `json:"cacheVariables"`
}

type cmakePresetFile struct {
	Configure []cmakePreset `json:"configurePresets"`
	Build     []cmakePreset `json:"buildPresets"`
	Test      []cmakePreset `json:"testPresets"`
}

func (p cmakePreset) parents() []string {
	var one string
	if json.Unmarshal(p.Inherits, &one) == nil && one != "" {
		return []string{one}
	}
	var many []string
	_ = json.Unmarshal(p.Inherits, &many)
	return many
}

// loadCMakePresets reads CMakePresets.json and, when present,
// CMakeUserPresets.json. ok is false when the first file is missing or does
// not parse.
func loadCMakePresets(root string) (cmakePresetFile, bool) {
	var all cmakePresetFile
	data, err := os.ReadFile(filepath.Join(root, "CMakePresets.json"))
	if err != nil || json.Unmarshal(data, &all) != nil {
		return cmakePresetFile{}, false
	}
	if data, err := os.ReadFile(filepath.Join(root, "CMakeUserPresets.json")); err == nil {
		var user cmakePresetFile
		if json.Unmarshal(data, &user) == nil {
			all.Configure = append(all.Configure, user.Configure...)
			all.Build = append(all.Build, user.Build...)
			all.Test = append(all.Test, user.Test...)
		}
	}
	return all, true
}

// resolve returns the first non-empty value of get along the inheritance
// chain of the named preset (own value first, then each parent in order).
func resolvePreset(list []cmakePreset, name string, get func(cmakePreset) string, depth int) string {
	if depth > 16 {
		return ""
	}
	for _, p := range list {
		if p.Name != name {
			continue
		}
		if v := get(p); v != "" {
			return v
		}
		for _, parent := range p.parents() {
			if v := resolvePreset(list, parent, get, depth+1); v != "" {
				return v
			}
		}
		return ""
	}
	return ""
}

func cacheVar(list []cmakePreset, name, key string) string {
	return resolvePreset(list, name, func(p cmakePreset) string {
		raw, ok := p.CacheVariables[key]
		if !ok {
			return ""
		}
		var s string
		if json.Unmarshal(raw, &s) == nil {
			return s
		}
		var obj struct {
			Value json.RawMessage `json:"value"`
		}
		if json.Unmarshal(raw, &obj) == nil {
			_ = json.Unmarshal(obj.Value, &s)
		}
		return s
	}, 0)
}

// refersTo reports whether preset p targets the configure preset cfg, either
// directly or through an inherited configurePreset.
func refersTo(list []cmakePreset, p cmakePreset, cfg string) bool {
	return resolvePreset(list, p.Name, func(q cmakePreset) string { return q.ConfigurePreset }, 0) == cfg
}

func firstFor(list []cmakePreset, cfg string) string {
	for _, p := range list {
		if !p.Hidden && refersTo(list, p, cfg) {
			return p.Name
		}
	}
	return ""
}

var shellSafe = regexp.MustCompile(`^[A-Za-z0-9_@%+=:,./-]+$`)

func shellQuote(s string) string {
	if shellSafe.MatchString(s) {
		return s
	}
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// cmakeTools returns the cmake and ctest commands. A CMAKE_MAKE_PROGRAM that
// lives under .../Tools/<X>/... selects <Tools>/CMake/bin when it exists.
func cmakeTools(makeProgram string) (cmake, ctest string) {
	cmake, ctest = "cmake", "ctest"
	if makeProgram == "" {
		return
	}
	for dir := filepath.Dir(makeProgram); dir != filepath.Dir(dir); dir = filepath.Dir(dir) {
		if filepath.Base(filepath.Dir(dir)) != "Tools" {
			continue
		}
		bin := filepath.Join(filepath.Dir(dir), "CMake", "bin")
		if exists(filepath.Join(bin, "cmake")) {
			cmake = shellQuote(filepath.Join(bin, "cmake"))
			if exists(filepath.Join(bin, "ctest")) {
				ctest = shellQuote(filepath.Join(bin, "ctest"))
			}
		}
		return
	}
	return
}

// cmakePresetCommands derives build and test commands from the project's
// CMake presets. ok is false when there are no usable presets.
func cmakePresetCommands(root string) (build string, test []string, ok bool) {
	f, found := loadCMakePresets(root)
	if !found {
		return "", nil, false
	}
	var cfgName, buildName string
	for _, p := range f.Configure {
		if p.Hidden {
			continue
		}
		if cfgName == "" {
			cfgName = p.Name
		}
		if b := firstFor(f.Build, p.Name); b != "" {
			cfgName, buildName = p.Name, b
			break
		}
	}
	if cfgName == "" {
		return "", nil, false
	}
	binDir := resolvePreset(f.Configure, cfgName, func(p cmakePreset) string { return p.BinaryDir }, 0)
	if binDir != "" {
		binDir = strings.NewReplacer("${sourceDir}", root, "${presetName}", cfgName,
			"${sourceDirName}", filepath.Base(root), "${sourceParentDir}", filepath.Dir(root)).Replace(binDir)
		if !filepath.IsAbs(binDir) {
			binDir = filepath.Join(root, binDir)
		}
		if rel, err := filepath.Rel(root, binDir); err == nil && !strings.HasPrefix(rel, "..") {
			binDir = rel
		}
	}
	testName := firstFor(f.Test, cfgName)
	if (buildName == "" || testName == "") && binDir == "" {
		return "", nil, false
	}
	cmake, ctest := cmakeTools(cacheVar(f.Configure, cfgName, "CMAKE_MAKE_PROGRAM"))
	build = cmake + " --preset " + shellQuote(cfgName) + " && " + cmake + " --build "
	if buildName != "" {
		build += "--preset " + shellQuote(buildName)
	} else {
		build += shellQuote(binDir)
	}
	if testName != "" {
		test = []string{ctest + " --preset " + shellQuote(testName)}
	} else {
		test = []string{ctest + " --test-dir " + shellQuote(binDir) + " --output-on-failure"}
	}
	return build, test, true
}
