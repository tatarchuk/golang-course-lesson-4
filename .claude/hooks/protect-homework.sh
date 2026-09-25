#!/usr/bin/env bash
# PreToolUse hook. Denies tool calls that write to protected files.
# CLAUDE.md in this repository prohibits Claude from writing the homework.
# The script always exits 0. A deny is a JSON decision on stdout.
set -u

files='pipeline/pipeline\.go|deadlock/fixed\.go|pipeline/pipeline_test\.go|deadlock/fixed_test\.go|workerpool/workerpool_test\.go|deadlock-analysis\.md|\.github/workflows/tests\.yml'

# File tools: the path must end with a protected file.
path_re="(^|/)(${files})$"

# Shell: a write operation followed by a protected file in the same command segment.
writers='(>{1,2}\|?\s*[^\s]*|\btee\b(\s+-[a-zA-Z]+)*\s+[^\s]*|\b(sed|perl)\b[^|;\n]*\s(-[a-zA-Z]*i|--in-place)[^|;\n]*|\b(cp|mv|dd|truncate|patch|python3?|git\s+apply)\b[^|;\n]*)'
cmd_re="${writers}(${files})"

decision=$(jq -r --arg cmd_re "$cmd_re" --arg path_re "$path_re" '
  if .tool_name == "Bash" then
    ((.tool_input.command // "") | test($cmd_re))
  else
    ((.tool_input.file_path // .tool_input.notebook_path // "") | test($path_re))
  end' 2>/dev/null) || exit 0

if [ "$decision" = "true" ]; then
  jq -cn '{hookSpecificOutput:{hookEventName:"PreToolUse",permissionDecision:"deny",permissionDecisionReason:"Denied by .claude/hooks/protect-homework.sh: this call writes to a protected file. CLAUDE.md prohibits Claude from writing this file. Only the user changes this file. CLAUDE.md lists the help that Claude can give."}}'
fi
exit 0
