#!/bin/bash
INPUT=$(cat)
COMMAND=$(echo "$INPUT" | jq -r '.tool_input.command // empty')

# Only block git commit commands
if ! echo "$COMMAND" | grep -qE '(^|;|\|&)\s*git commit'; then
  exit 0
fi

echo "Blocking git commit — running make test first..." >&2
make -C /home/emil/Source/emilhauk/msg test

if [ $? -ne 0 ]; then
  echo "make test failed. Commit aborted." >&2
  exit 2
fi

echo "Tests passed." >&2
exit 0
