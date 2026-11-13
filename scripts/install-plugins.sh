#!/usr/bin/env bash
# Install a curated set of Claude-format plugins for decsr and write the
# "plugins" profile that maps their skills to workspace roles.
#
#   scripts/install-plugins.sh [--dry-run] [--uninstall] [--help]
set -euo pipefail

DECSR_DIR="$HOME/.decsr"
PLUGINS_DIR="$DECSR_DIR/plugins"
SRC_DIR="$DECSR_DIR/plugin-src"
PROFILE="$DECSR_DIR/profiles/plugins.yaml"
CONFIG="$DECSR_DIR/config.yaml"

# owner/repo | plugin dir inside the repo ("." = repo root) | plugin name
PLUGINS=(
  "obra/superpowers|.|superpowers"
  "nextlevelbuilder/ui-ux-pro-max-skill|.|ui-ux-pro-max"
  "trailofbits/skills|plugins/modern-cpp|modern-cpp"
  "trailofbits/skills|plugins/modern-python|modern-python"
  "trailofbits/skills|plugins/insecure-defaults|insecure-defaults"
  "trailofbits/skills|plugins/differential-review|differential-review"
  "anthropics/claude-plugins-official|plugins/feature-dev|feature-dev"
  "anthropics/claude-plugins-official|plugins/pr-review-toolkit|pr-review-toolkit"
)

# role | skill ids (<plugin>:<skill dir>)
ROLE_SKILLS=(
  "pm|superpowers:brainstorming superpowers:writing-plans"
  "coder|superpowers:test-driven-development modern-cpp:modern-cpp modern-python:modern-python"
  "coder_fixer|superpowers:systematic-debugging"
  "qa|superpowers:verification-before-completion superpowers:test-driven-development"
  "security|differential-review:differential-review"
  "ux_reviewer|ui-ux-pro-max:ui-ux-pro-max"
)

DRY=0 UNINSTALL=0
for arg in "$@"; do
  case "$arg" in
    --dry-run) DRY=1 ;;
    --uninstall) UNINSTALL=1 ;;
    -h | --help)
      cat <<'USAGE'
Usage: install-plugins.sh [--dry-run] [--uninstall] [--help]

Installs these plugins into ~/.decsr/plugins and writes the profile
~/.decsr/profiles/plugins.yaml. For each built-in role it lists only skills
(the role's existing skills from ~/.decsr/config.yaml, then the plugin ones)
and read_only (copied when true). Runner, model, effort and description are
left out, so applying the profile keeps the project's own values.

  superpowers, ui-ux-pro-max, modern-cpp, modern-python, insecure-defaults,
  differential-review, feature-dev, pr-review-toolkit

  --dry-run    print what would happen, change nothing
  --uninstall  remove only those plugins, their clones in ~/.decsr/plugin-src
               and the plugins profile
  --help       show this help

Needs git and network access.
USAGE
      exit 0
      ;;
    *) echo "unknown option: $arg (try --help)" >&2; exit 2 ;;
  esac
done

run() { # run a mutating command, or just print it with --dry-run
  if ((DRY)); then echo "  [dry-run] $*"; else "$@"; fi
}

# ---- uninstall -------------------------------------------------------------
if ((UNINSTALL)); then
  echo "Removing plugins installed by this script..."
  for entry in "${PLUGINS[@]}"; do
    IFS='|' read -r _ _ name <<<"$entry"
    [[ -d "$PLUGINS_DIR/$name" ]] && { echo "  - $name"; run rm -rf "${PLUGINS_DIR:?}/$name"; }
  done
  for entry in "${PLUGINS[@]}"; do
    IFS='|' read -r repo _ _ <<<"$entry"
    clone="$SRC_DIR/${repo//\//-}"
    [[ -d "$clone" ]] && { echo "  - clone ${clone/#$HOME/\~}"; run rm -rf "$clone"; }
  done
  [[ -f "$PROFILE" ]] && { echo "  - profile plugins"; run rm -f "$PROFILE"; }
  ((DRY)) || rmdir "$SRC_DIR" 2>/dev/null || true
  echo "Done."
  exit 0
fi

command -v git >/dev/null || { echo "error: git is required" >&2; exit 1; }

# ---- fetch + copy ----------------------------------------------------------
clone_dir() { echo "$SRC_DIR/${1//\//-}"; } # clone_dir owner/repo

fetch_repo() { # fetch_repo owner/repo: clone or update; returns 1 on failure
  local repo=$1 dir
  dir=$(clone_dir "$repo")
  if ((DRY)); then
    echo "  [dry-run] git clone --depth 1 https://github.com/$repo.git ${dir/#$HOME/\~}"
  elif [[ -d "$dir/.git" ]]; then
    git -C "$dir" fetch --quiet --depth 1 origin || return 1
    git -C "$dir" reset --quiet --hard FETCH_HEAD || return 1
  else
    mkdir -p "$SRC_DIR" || return 1
    rm -rf "$dir"
    git clone --quiet --depth 1 "https://github.com/$repo.git" "$dir" || return 1
  fi
}

echo "Fetching plugins into ${SRC_DIR/#$HOME/\~} ..."
CLONED=" " # repos fetched so far, space separated (no associative arrays: bash 3.2)
INSTALLED=()
for entry in "${PLUGINS[@]}"; do
  IFS='|' read -r repo sub name <<<"$entry"
  if [[ $CLONED != *" $repo "* ]]; then
    fetch_repo "$repo" || { echo "error: could not fetch https://github.com/$repo.git" >&2; exit 1; }
    CLONED+="$repo "
  fi
  from="$(clone_dir "$repo")/$sub"
  to="$PLUGINS_DIR/$name"
  if ((DRY)); then
    echo "  [dry-run] copy $repo:$sub -> ${to/#$HOME/\~} (without .git)"
    INSTALLED+=("$name")
    continue
  fi
  if [[ ! -f "$from/.claude-plugin/plugin.json" ]]; then
    echo "  warning: $repo:$sub has no .claude-plugin/plugin.json, skipped" >&2
    continue
  fi
  mkdir -p "$PLUGINS_DIR"
  rm -rf "${to:?}"
  mkdir -p "$to"
  (cd "$from" && tar --exclude=.git -cf - .) | (cd "$to" && tar -xf -)
  INSTALLED+=("$name")
done

# ---- skill discovery and checks -------------------------------------------
plugin_skill_roots() { # plugin_skill_roots plugin: skill dirs relative to the plugin, as the loader scans them
  local mf="$PLUGINS_DIR/$1/.claude-plugin/plugin.json" v rel
  echo skills
  [[ -f "$mf" && ! -L "$mf" ]] || return 0
  # the manifest "skills" value: one string or an array of strings
  v=$(tr -d '\n' <"$mf" | sed -n 's/.*"skills"[[:space:]]*:[[:space:]]*\(\[[^]]*\]\|"[^"]*"\).*/\1/p' | grep -o '"[^"]*"' | tr -d '"') || true
  for rel in $v; do
    rel=${rel#./}
    rel=${rel%/}
    case "$rel" in "" | . | .. | /* | ../* | */.. | */../*) continue ;; esac
    echo "$rel"
  done
}

skill_file() { # skill_file plugin skill -> SKILL.md path if it exists and is no symlink
  local d f
  for d in $(plugin_skill_roots "$1"); do
    f="$PLUGINS_DIR/$1/$d/$2/SKILL.md"
    [[ -f "$f" && ! -L "$f" && ! -L "$PLUGINS_DIR/$1/$d/$2" ]] && { echo "$f"; return 0; }
  done
  return 1
}

count_skills() { # count_skills plugin
  local n=0 d f
  for d in $(plugin_skill_roots "$1"); do
    for f in "$PLUGINS_DIR/$1/$d"/*/SKILL.md; do [[ -f "$f" && ! -L "$f" ]] && n=$((n + 1)); done
  done
  echo "$n"
}

count_md() { # count_md plugin dir -> number of markdown files in the plugin dir
  local n=0 f
  for f in "$PLUGINS_DIR/$1/$2"/*.md; do [[ -f "$f" ]] && n=$((n + 1)); done
  echo "$n"
}

# ---- profile ---------------------------------------------------------------
# Applying a profile overwrites read_only and skills of every role it lists
# (empty values included), so copy the current values of those from the global
# config. Runner, model, effort and description are left out: an empty one in a
# profile keeps what the project has.
role_fields() { # role_fields <role>: "key<TAB>value" lines from config.yaml agents:
  [[ -f "$CONFIG" ]] || return 0
  awk -v role="$1" '
    /^agents:[[:space:]]*$/ { inside = 1; next }
    inside && /^[^[:space:]#]/ { inside = 0 }
    !inside || /^[[:space:]]*(#|$)/ { next }
    {
      match($0, /^ */); ind = RLENGTH
      line = substr($0, ind + 1)
      if (!rind) rind = ind
      if (ind == rind) { cur = line; sub(/:.*/, "", cur); next }
      if (cur != role) next
      if (line ~ /^- /) { if (sec == "skills") print "skills\t" substr(line, 3); next }
      key = line; sub(/:.*/, "", key)
      val = line; sub(/^[^:]*:[[:space:]]*/, "", val)
      sec = key
      if (key == "read_only" && val ~ /^true([[:space:]]|$)/)
        print key "\ttrue"
    }' "$CONFIG"
}

build_profile() {
  local entry role ids id plugin skill key val fields skills_out kept
  echo "agents:"
  for entry in "${ROLE_SKILLS[@]}"; do
    IFS='|' read -r role ids <<<"$entry"
    kept=()
    for id in $ids; do
      plugin=${id%%:*} skill=${id#*:}
      if skill_file "$plugin" "$skill" >/dev/null; then
        kept+=("$id")
      else
        echo "warning: skill $id not found, dropped from role $role" >&2
      fi
    done
    fields=$(role_fields "$role")
    skills_out=""
    echo "    $role:"
    while IFS=$'\t' read -r key val; do
      [[ -z "$key" ]] && continue
      if [[ $key == skills ]]; then skills_out+="$val"$'\n'; else echo "        $key: $val"; fi
    done <<<"$fields"
    # keep the role's own file-based skills, then add the plugin ones
    if [[ -n "$skills_out" || ${#kept[@]} -gt 0 ]]; then
      echo "        skills:"
      [[ -n "$skills_out" ]] && while IFS= read -r val; do [[ -n "$val" ]] && echo "            - $val"; done <<<"$skills_out"
      for id in ${kept[@]+"${kept[@]}"}; do echo "            - $id"; done
    fi
  done
}

echo
if ((DRY)); then
  echo "[dry-run] would write ${PROFILE/#$HOME/\~} with skills for: pm coder coder_fixer qa security ux_reviewer"
  echo "[dry-run] nothing was changed."
  exit 0
fi

mkdir -p "$(dirname "$PROFILE")"
build_profile >"$PROFILE.tmp"
mv "$PROFILE.tmp" "$PROFILE"

echo "Installed ${#INSTALLED[@]} plugins into ${PLUGINS_DIR/#$HOME/\~}:"
for name in ${INSTALLED[@]+"${INSTALLED[@]}"}; do
  printf '  %-22s %2s skills, %2s commands, %2s agents\n' "$name" "$(count_skills "$name")" \
    "$(count_md "$name" commands)" "$(count_md "$name" agents)"
done
echo
echo "Wrote profile ${PROFILE/#$HOME/\~}:"
sed 's/^/  /' "$PROFILE"
echo
echo "Next: open decsr, run /plugin to see them, /profile plugins to apply."
