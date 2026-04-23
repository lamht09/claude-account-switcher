# claude-account-switcher

`claude-account-switcher` (CLI command: `ca`) is a fast local tool to switch between multiple Claude accounts on one machine.

It helps you:
- keep multiple Claude accounts in a local rotation set;
- switch accounts quickly by slot index or email;
- inspect status/usage and token diagnostics;
- recover backup metadata when needed.

## How It Works

The tool reads your active Claude credential config, stores account backups locally, then updates only the live `oauthAccount` block when switching.

By default:
- Claude config directory: `~/.claude` (or `CLAUDE_CONFIG_DIR` if set)
- Live credential file: `~/.claude/.credentials.json`
- Backup root: `~/.claude-account-backup`

Your current login session is not deleted by normal operations. After switching, restart Claude Code so it loads the new authentication data.

## Requirements

- Go `1.24+` (if building from source)
- A signed-in Claude account on the machine (for first account import)

## Installation

### Option 1: Install from release script (recommended)

#### Linux / macOS

```bash
curl -fsSL https://raw.githubusercontent.com/lamht09/claude-account-switcher/main/install.sh | sh
```

#### Windows PowerShell

```powershell
iwr https://raw.githubusercontent.com/lamht09/claude-account-switcher/main/install.ps1 -UseBasicParsing | iex
```

#### Windows CMD

```bat
curl -fsSL -o install.cmd https://raw.githubusercontent.com/lamht09/claude-account-switcher/main/install.cmd
install.cmd
```

Notes:
- The installer verifies checksums from `SHA256SUMS`.
- Default install location is `$HOME/.local/bin` (or `%USERPROFILE%\.local\bin` on Windows).
- On Windows, `install.ps1` / `install.cmd` append the install directory to your **user** `PATH` if it is not already there (so `ca` works like on many Mac/Linux setups). Set `SKIP_PATH=1` to skip. New terminals and IDEs may need a restart to see the updated `PATH`.
- You can override:
  - `REPO` (default: `lamht09/claude-account-switcher`)
  - `VERSION` (default: `latest`)
  - `INSTALL_DIR` (default: platform path above)

### Option 2: Build from source

#### Linux / macOS

```bash
git clone https://github.com/lamht09/claude-account-switcher.git
cd claude-account-switcher
make build
install ./ca "$HOME/.local/bin/ca"
```

#### Windows (PowerShell + make available)

```powershell
git clone https://github.com/lamht09/claude-account-switcher.git
cd claude-account-switcher
make build
New-Item -ItemType Directory -Force -Path "$HOME\.local\bin" | Out-Null
Copy-Item .\ca.exe "$HOME\.local\bin\ca.exe" -Force
```

### Available Makefile build targets

Use these targets when you need binaries for specific platforms:

- `make build` - local binary (`./ca` or `.\ca.exe`)
- `make build-env` - build all supported OS/ARCH binaries into `dist/`
- `make build-linux-amd64`
- `make build-linux-arm64`
- `make build-macos-amd64`
- `make build-macos-arm64`
- `make build-windows-amd64`
- `make build-windows-arm64`
- `make release-local` - multi-OS archives + `SHA256SUMS`
- `make clean` - remove `dist/` and local binary

## Quick Start

### Linux / macOS

```bash
# 1) Check current active account from Claude config
ca status

# 2) Save current account into rotation set (auto slot or explicit slot)
ca add
ca add --slot 2

# 3) View saved accounts
ca list
ca list --token-status

# 4) Switch account
ca switch          # next account in rotation
ca switch-to 2     # by slot
ca switch-to user@example.com
```

### Windows PowerShell

```powershell
# 1) Check current active account from Claude config
ca status

# 2) Save current account into rotation set (auto slot or explicit slot)
ca add
ca add --slot 2

# 3) View saved accounts
ca list
ca list --token-status

# 4) Switch account
ca switch
ca switch-to 2
ca switch-to user@example.com
```

If this is your first run and there are no slots yet, `ca list` will guide you to bootstrap from the current active account.

## Command Reference

### Global flags

- `--version` print CLI version
- `--debug` enable debug output

### Actions

#### `ca status`

Show the account currently active in Claude config and related diagnostics.

#### `ca list [--token-status]`

List all backed-up accounts with rotation details.

- `--token-status`: include token diagnostics in output.

#### `ca add [--slot N]`

Add the current live account from Claude config into backup storage.

- `--slot N`: place/overwrite at a specific slot (`N >= 0`).

#### `ca switch`

Switch to the next account in rotation order.

Self-healing behavior: if the active account is missing from rotation set, `switch` adds it first and asks for one more `switch`.

#### `ca switch-to <slot|email>`

Switch directly to a specific account by slot number or email.

#### `ca remove <slot|email>`

Remove one backup account by slot or email (with confirmation).

#### `ca purge`

Delete all local `claude-swap` data from this machine (with confirmation). Current live Claude login session is kept intact.

#### `ca repair`

Repair and rebuild backup metadata from current stored data.

## Safety and Validation

- Risky actions (`remove`, `purge`, slot overwrite in `add`) ask for confirmation.
- In non-interactive environments (no TTY), risky actions fail closed for safety.
- Exactly one action is required per command invocation.
- Flag constraints:
  - `--token-status` is valid only with `list`.
  - `--slot` is valid only with `add`.
- Root safety check:
  - The tool refuses running as root (except when container runtime is detected).

## Typical Workflows

### Daily switching

```bash
ca list
ca switch
```

### Targeted switching for a specific account

```bash
ca switch-to user@example.com
```

### Replace an outdated slot with current logged-in account

```bash
ca add --slot 3
```

### Cleanup local switcher data

```bash
ca purge
```

## Development

### Linux / macOS

Run tests:

```bash
make test
```

Build local binary:

```bash
make build
```

Build specific targets (examples):

```bash
make build-linux-amd64
make build-macos-arm64
make build-windows-amd64
make build-env
```

Run directly with Go:

```bash
go run ./cmd/ca --version
go run ./cmd/ca status
go run ./cmd/ca list --token-status
go run ./cmd/ca add --slot 2
go run ./cmd/ca switch
go run ./cmd/ca switch-to 2
go run ./cmd/ca remove 2
go run ./cmd/ca purge
go run ./cmd/ca repair
```

### Windows PowerShell

Run tests/build:

```powershell
make test
make build
```

Build specific targets (examples):

```powershell
make build-windows-amd64
make build-windows-arm64
make build-env
```

Run directly with Go:

```powershell
go run ./cmd/ca --version
go run ./cmd/ca status
go run ./cmd/ca list --token-status
go run ./cmd/ca add --slot 2
go run ./cmd/ca switch
go run ./cmd/ca switch-to 2
go run ./cmd/ca remove 2
go run ./cmd/ca purge
go run ./cmd/ca repair
```

## Troubleshooting

- `cannot switch: no active account in Claude Code`
  - Sign in via Claude Code first, then run `ca add` and retry.
- Command not found after install
  - Ensure your install directory is in `PATH` (`$HOME/.local/bin` or `%USERPROFILE%\.local\bin`).
- Switched account but Claude still shows old account
  - Restart Claude Code to reload authentication files.

## License

MIT License. See `LICENSE`.
