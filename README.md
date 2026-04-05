# Gode

`gode` is a terminal coding assistant with a TUI frontend.

By default it starts with a local MLX backend:

- `provider`: `mlx_vlm`
- `model`: `mlx-community/gemma-4-31b-8bit`

## Prerequisites

- Go installed
- A working Python environment with `mlx-vlm` available
- Your local model installed or downloadable by MLX

If `mlx-vlm` is not on your default `python3`, point Gode at the right interpreter:

```bash
export GODE_MLX_PYTHON=/usr/local/bin/python3
```

## Start The CLI

Build and run:

```bash
make run
```

Install `gode` globally for your user so you can run it from any directory:

```bash
make install
gode
```

Or run directly with Go:

```bash
go run ./cmd/gode
```

Or build the binary yourself:

```bash
make build
./bin/gode
```

## Common Flags

Start the TUI with an explicit provider or model override:

```bash
./bin/gode --provider mlx_vlm --model mlx-community/gemma-4-31b-8bit
```

Run a single prompt without the TUI:

```bash
./bin/gode -p "Summarize this repository."
```

Run a single prompt headless and explicitly allow tool execution:

```bash
./bin/gode -p "Inspect this repository and fix the failing test." --yes
```

Print the version:

```bash
./bin/gode --version
```

## Configuration

Global config file:

```text
~/.config/gode/gode.json
```

Project config file:

```text
.gode/gode.json
```

Example config:

```json
{
  "provider": "mlx_vlm",
  "model": "mlx-community/gemma-4-31b-8bit",
  "context_tokens": 32768,
  "max_tokens": 4096,
  "providers": {
    "mlx_vlm": {
      "python": "/usr/local/bin/python3"
    }
  }
}
```

## TUI Commands

Inside the app:

- `/help` shows commands and shortcuts
- `/status` shows provider, model, and memory budget
- `/compact` forces transcript compaction
- `/new` starts a fresh session
- `/sessions` lists recent sessions

## Notes

- Sessions are stored in `~/.config/gode/gode.db`
- Project instructions can be loaded from `.gode/GODE.md`, `GODE.md`, or `~/.config/gode/GODE.md`
- The app auto-compacts history when transcript memory crosses half of `context_tokens`
