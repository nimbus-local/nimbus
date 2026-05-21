#!/usr/bin/env sh
# install.sh — installs the nimbuslocal CLI
# Usage: curl -fsSL https://raw.githubusercontent.com/nimbus-local/nimbus/master/install.sh | sh
set -e

REPO="nimbus-local/nimbus"
BINARY="nimbuslocal"
INSTALL_DIR="${HOME}/.local/bin"
MARKER_BEGIN="### MANAGED BY NIMBUSLOCAL START (DO NOT EDIT)"
MARKER_END="### MANAGED BY NIMBUSLOCAL END (DO NOT EDIT)"

# ── Detect OS ────────────────────────────────────────────────────────────────

OS=$(uname -s | tr '[:upper:]' '[:lower:]')
case "$OS" in
  linux)  OS="linux"  ;;
  darwin) OS="darwin" ;;
  *)
    echo "Unsupported OS: $OS"
    exit 1
    ;;
esac

# ── Detect architecture ───────────────────────────────────────────────────────

ARCH=$(uname -m)
case "$ARCH" in
  x86_64|amd64)   ARCH="amd64" ;;
  arm64|aarch64)  ARCH="arm64" ;;
  *)
    echo "Unsupported architecture: $ARCH"
    exit 1
    ;;
esac

# ── Download ──────────────────────────────────────────────────────────────────

ASSET="${BINARY}-${OS}-${ARCH}"
URL="https://github.com/${REPO}/releases/latest/download/${ASSET}"

echo "Installing nimbuslocal (${OS}/${ARCH})..."
mkdir -p "$INSTALL_DIR"

if command -v curl >/dev/null 2>&1; then
  curl -fsSL "$URL" -o "${INSTALL_DIR}/${BINARY}"
elif command -v wget >/dev/null 2>&1; then
  wget -qO "${INSTALL_DIR}/${BINARY}" "$URL"
else
  echo "Error: curl or wget is required."
  exit 1
fi

chmod +x "${INSTALL_DIR}/${BINARY}"
echo "Installed to ${INSTALL_DIR}/${BINARY}"

# ── PATH setup ────────────────────────────────────────────────────────────────

# Already in PATH — nothing to do.
case ":${PATH}:" in
  *":${INSTALL_DIR}:"*)
    echo "nimbuslocal is ready."
    exit 0
    ;;
esac

# Detect shell profile.
SHELL_NAME=$(basename "${SHELL:-sh}")
case "$SHELL_NAME" in
  zsh)
    PROFILE="${ZDOTDIR:-$HOME}/.zshrc"
    PATH_LINE="export PATH=\"\$PATH:${INSTALL_DIR}\""
    ;;
  bash)
    if [ "$OS" = "darwin" ]; then
      PROFILE="${HOME}/.bash_profile"
    else
      PROFILE="${HOME}/.bashrc"
    fi
    PATH_LINE="export PATH=\"\$PATH:${INSTALL_DIR}\""
    ;;
  fish)
    PROFILE="${HOME}/.config/fish/config.fish"
    PATH_LINE="fish_add_path ${INSTALL_DIR}"
    ;;
  *)
    PROFILE="${HOME}/.profile"
    PATH_LINE="export PATH=\"\$PATH:${INSTALL_DIR}\""
    ;;
esac

BLOCK=$(printf '\n%s\n%s\n%s\n' "$MARKER_BEGIN" "$PATH_LINE" "$MARKER_END")

if [ -f "$PROFILE" ] && grep -qF "$MARKER_BEGIN" "$PROFILE"; then
  # Replace existing managed block.
  tmp=$(mktemp)
  awk "
    /$MARKER_BEGIN/ { skip=1 }
    !skip { print }
    /$MARKER_END/   { skip=0 }
  " "$PROFILE" > "$tmp"
  printf '%s\n' "$BLOCK" >> "$tmp"
  mv "$tmp" "$PROFILE"
  echo "Updated PATH block in ${PROFILE}."
else
  printf '%s\n' "$BLOCK" >> "$PROFILE"
  echo "Added PATH block to ${PROFILE}."
fi

echo ""
echo "nimbuslocal installed successfully!"
echo "Restart your terminal or run:  source ${PROFILE}"
echo ""
echo "To uninstall:"
echo "  rm ${INSTALL_DIR}/${BINARY}"
echo "  Remove the ${MARKER_BEGIN} block from ${PROFILE}"
