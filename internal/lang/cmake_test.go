package lang

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o755); err != nil {
		t.Fatal(err)
	}
}

const defaultBuild = "cmake -S . -B build && cmake --build build"

var defaultTest = []string{"ctest --test-dir build --output-on-failure"}

func TestCMakeNoPresets(t *testing.T) {
	got := CommandsFor("cpp", t.TempDir())
	if got.Build != defaultBuild || !reflect.DeepEqual(got.Test, defaultTest) {
		t.Errorf("got %+v", got)
	}
}

func TestCMakePresetsWithBuildPreset(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "CMakePresets.json"), `{
 "version": 3,
 "configurePresets": [
  {"name": "base", "hidden": true, "binaryDir": "${sourceDir}/hidden"},
  {"name": "debug", "binaryDir": "${sourceDir}/out/debug"},
  {"name": "release", "binaryDir": "${sourceDir}/build"}
 ],
 "buildPresets": [{"name": "rel", "configurePreset": "release"}]
}`)
	got := CommandsFor("cpp", root)
	if want := "cmake --preset release && cmake --build --preset rel"; got.Build != want {
		t.Errorf("build = %q, want %q", got.Build, want)
	}
	if want := []string{"ctest --test-dir build --output-on-failure"}; !reflect.DeepEqual(got.Test, want) {
		t.Errorf("test = %v, want %v", got.Test, want)
	}
}

func TestCMakePresetsNoBuildPreset(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "CMakePresets.json"), `{
 "configurePresets": [{"name": "dev", "binaryDir": "${sourceDir}/out/${presetName}"}]
}`)
	got := CommandsFor("c", root)
	if want := "cmake --preset dev && cmake --build out/dev"; got.Build != want {
		t.Errorf("build = %q, want %q", got.Build, want)
	}
	if want := []string{"ctest --test-dir out/dev --output-on-failure"}; !reflect.DeepEqual(got.Test, want) {
		t.Errorf("test = %v, want %v", got.Test, want)
	}
}

func TestCMakePresetsWithTestPreset(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "CMakePresets.json"), `{
 "configurePresets": [{"name": "release", "binaryDir": "${sourceDir}/build"}],
 "buildPresets": [{"name": "release", "configurePreset": "release"}],
 "testPresets": [{"name": "all", "configurePreset": "release"}]
}`)
	got := CommandsFor("cpp", root)
	if want := []string{"ctest --preset all"}; !reflect.DeepEqual(got.Test, want) {
		t.Errorf("test = %v, want %v", got.Test, want)
	}
}

func TestCMakePresetsInherits(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "CMakePresets.json"), `{
 "configurePresets": [
  {"name": "base", "hidden": true, "binaryDir": "${sourceDir}/b", "cacheVariables": {"CMAKE_MAKE_PROGRAM": {"type": "FILEPATH", "value": "`+filepath.Join(root, "Tools", "Ninja", "ninja")+`"}}},
  {"name": "child", "inherits": ["base"]}
 ],
 "buildPresets": [{"name": "b1", "configurePreset": "child"}]
}`)
	bin := filepath.Join(root, "Tools", "CMake", "bin")
	writeFile(t, filepath.Join(bin, "cmake"), "#!/bin/sh\n")
	writeFile(t, filepath.Join(bin, "ctest"), "#!/bin/sh\n")
	got := CommandsFor("cpp", root)
	cm := filepath.Join(bin, "cmake")
	if want := cm + " --preset child && " + cm + " --build --preset b1"; got.Build != want {
		t.Errorf("build = %q, want %q", got.Build, want)
	}
	if want := []string{filepath.Join(bin, "ctest") + " --test-dir b --output-on-failure"}; !reflect.DeepEqual(got.Test, want) {
		t.Errorf("test = %v, want %v", got.Test, want)
	}
}

func TestCMakeToolsDetection(t *testing.T) {
	dir := t.TempDir()
	ninja := filepath.Join(dir, "Tools", "Ninja", "ninja")
	if c, _ := cmakeTools(ninja); c != "cmake" {
		t.Errorf("without Tools/CMake got %q", c)
	}
	bin := filepath.Join(dir, "Tools", "CMake", "bin")
	writeFile(t, filepath.Join(bin, "cmake"), "x")
	c, ct := cmakeTools(ninja)
	if c != filepath.Join(bin, "cmake") || ct != "ctest" {
		t.Errorf("got %q %q", c, ct)
	}
	writeFile(t, filepath.Join(bin, "ctest"), "x")
	if _, ct := cmakeTools(ninja); ct != filepath.Join(bin, "ctest") {
		t.Errorf("ctest = %q", ct)
	}
	if c, _ := cmakeTools("/usr/bin/make"); c != "cmake" {
		t.Errorf("plain path got %q", c)
	}
}

func TestCMakeUserPresets(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "CMakePresets.json"), `{"version": 3}`)
	writeFile(t, filepath.Join(root, "CMakeUserPresets.json"), `{"configurePresets": [{"name": "mine", "binaryDir": "${sourceDir}/u"}]}`)
	if got := CommandsFor("cpp", root); got.Build != "cmake --preset mine && cmake --build u" {
		t.Errorf("build = %q", got.Build)
	}
}

func TestCMakeBadJSONFallsBack(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "CMakePresets.json"), `{not json`)
	got := CommandsFor("cpp", root)
	if got.Build != defaultBuild || !reflect.DeepEqual(got.Test, defaultTest) {
		t.Errorf("got %+v", got)
	}
}
