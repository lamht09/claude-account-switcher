#!/usr/bin/env sh
set -eu

REPO="${REPO:-lamht09/claude-account-switcher}"
VERSION="${VERSION:-latest}"
BIN_NAME="ca"
INSTALL_DIR="${INSTALL_DIR:-$HOME/.local/bin}"
TMP_DIR="$(mktemp -d)"
trap 'rm -rf "$TMP_DIR"' EXIT

OS="$(uname -s | tr '[:upper:]' '[:lower:]')"
ARCH="$(uname -m)"
case "$ARCH" in
  x86_64) ARCH="amd64" ;;
  aarch64|arm64) ARCH="arm64" ;;
esac
# Release tarballs use "macos" (see Makefile), not "darwin" from uname.
case "$OS" in
  darwin) OS="macos" ;;
esac

if [ "$VERSION" = "latest" ]; then
  api_url="https://api.github.com/repos/$REPO/releases/latest"
  # Single-line JSON so sed can match (GitHub may return pretty-printed JSON).
  if [ -n "${GITHUB_TOKEN:-}" ]; then
    VERSION="$(curl -fsSL -H "Authorization: Bearer $GITHUB_TOKEN" "$api_url" | tr -d '\n\r' | sed -n 's/.*"tag_name"[[:space:]]*:[[:space:]]*"\([^"]*\)".*/\1/p')"
  else
    VERSION="$(curl -fsSL "$api_url" | tr -d '\n\r' | sed -n 's/.*"tag_name"[[:space:]]*:[[:space:]]*"\([^"]*\)".*/\1/p')"
  fi
  if [ -z "$VERSION" ]; then
    echo "Failed to resolve latest release tag for $REPO (check REPO, network, or set GITHUB_TOKEN if rate-limited)." >&2
    exit 1
  fi
fi

# Tarball layout matches Makefile release-local (binary named ca_<os>_<arch>).
BIN_IN_ARCHIVE="ca_${OS}_${ARCH}"
ASSET="${BIN_IN_ARCHIVE}.tar.gz"
BASE_URL="https://github.com/$REPO/releases/download/$VERSION"

curl -fsSL "$BASE_URL/SHA256SUMS" -o "$TMP_DIR/SHA256SUMS"
curl -fsSL "$BASE_URL/$ASSET" -o "$TMP_DIR/$ASSET"

if command -v sha256sum >/dev/null 2>&1; then
  (cd "$TMP_DIR" && sha256sum -c SHA256SUMS --ignore-missing)
elif command -v shasum >/dev/null 2>&1; then
  (cd "$TMP_DIR" && shasum -a 256 -c SHA256SUMS --ignore-missing)
else
  echo "Missing checksum tool: install sha256sum or shasum." >&2
  exit 1
fi

mkdir -p "$INSTALL_DIR"
tar -xzf "$TMP_DIR/$ASSET" -C "$TMP_DIR"
install "$TMP_DIR/$BIN_IN_ARCHIVE" "$INSTALL_DIR/$BIN_NAME"
echo "Installed $BIN_NAME to $INSTALL_DIR/$BIN_NAME"

# Persist PATH update for future shell sessions when needed.
PATH_EXPORT_LINE="export PATH=\"$INSTALL_DIR:\$PATH\""
if ! printf '%s' ":$PATH:" | grep -Fq ":$INSTALL_DIR:"; then
  shell_name="$(basename "${SHELL:-}")"
  case "$shell_name" in
    zsh) rc_file="$HOME/.zshrc" ;;
    bash) rc_file="$HOME/.bashrc" ;;
    *) rc_file="$HOME/.profile" ;;
  esac

  touch "$rc_file"
  if ! grep -Fq "$PATH_EXPORT_LINE" "$rc_file"; then
    printf '\n# Added by %s installer\n%s\n' "$BIN_NAME" "$PATH_EXPORT_LINE" >> "$rc_file"
    echo "Added $INSTALL_DIR to PATH in $rc_file"
    echo "Run: . \"$rc_file\""
  else
    echo "PATH entry already exists in $rc_file"
  fi
fi
