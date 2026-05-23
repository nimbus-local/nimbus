#!/bin/sh
# Installs the project git hooks into .git/hooks/.
# Run once after cloning: sh scripts/install-hooks.sh
set -e

cp scripts/pre-commit .git/hooks/pre-commit
chmod +x .git/hooks/pre-commit
echo "git hooks installed"
