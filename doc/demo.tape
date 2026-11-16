# DECSR demo recording (charmbracelet/vhs). Render with: make demo
#
# Run it from the decsr repository root with DECSR_DEMO_DIR set to a small
# throwaway project (a git repo): DECSR_DEMO_DIR=~/demo make demo. The hidden
# block below cds there inside the recorded shell, so decsr works on that
# project. The Output path is relative to where vhs is started.
#
# Model output is not deterministic: the Sleep timings below are guesses and
# will need tuning. Configure a cheap, fast model so the lead and the nodes
# finish inside them.

Output doc/assets/demo.gif

Set Shell "bash"
Set FontFamily "Monaspace Krypton"
Set FontSize 16
Set Width 1280
Set Height 760
Set Padding 20
Set TypingSpeed 40ms
Set Theme { "name": "Galaxy", "black": "#0b1426", "red": "#ff5c7a", "green": "#6ff2b4", "yellow": "#ffd166", "blue": "#4a86ff", "magenta": "#7cb8ff", "cyan": "#5ce1e6", "white": "#c9d8f5", "brightBlack": "#22335a", "brightRed": "#ff5c7a", "brightGreen": "#6ff2b4", "brightYellow": "#ffd166", "brightBlue": "#7cb8ff", "brightMagenta": "#bfe3ff", "brightCyan": "#5ce1e6", "brightWhite": "#e8f1ff", "background": "#0b1426", "foreground": "#c9d8f5", "selection": "#22335a", "cursor": "#5ce1e6" }

Require decsr

# Setup, not recorded: go to the demo project
Hide
Type `cd "$DECSR_DEMO_DIR" && clear`
Enter
Sleep 1s
Show

# Scene 1: check the setup
Type "decsr doctor"
Enter
Sleep 3s

# Scene 2: open the Workspace
Type "clear"
Enter
Type "decsr"
Enter
Sleep 3s

# Scene 3: give the lead a small task; it splits it into nodes
Type "add a --json flag to the version command"
Enter
Sleep 40s

# Scene 4: move to the graph (Tab), accept the suggested roles (A), run all (R)
Tab
Sleep 1s
Type "A"
Sleep 2s
Type "R"
Sleep 90s

# Scene 5: back to the prompt (Tab), merge the session branch, then quit
Tab
Sleep 1s
Type "/merge"
Enter
Sleep 3s
Ctrl+C
Sleep 500ms
Ctrl+C
Sleep 1s
