# Layercake

Layercake is a companion tool for Sandfire that builds VM root filesystems using a layered, inheritance-based approach.

## Building

```bash
cd layercake
go build -o layercake ./cmd/layercake
```

## How It Works

Layercake organizes root filesystems as **layers** that can inherit from each other:

- A **base layer** (with `PARENT=scratch`) bootstraps a minimal Ubuntu Linux system using debootstrap
- **Derived layers** inherit from a parent layer, copying its rootfs and applying additional customizations via a `layer.sh` script

This allows you to define a base Ubuntu image once, then create specialized variants (e.g., web server, dev tools) without rebuilding from scratch each time. When a parent layer changes, layercake detects staleness and rebuilds dependent layers.

Derived layers can also inherit from other derived layers, forming a chain. For example: `base-ubuntu24` → `web` → `django` → `some-cool-project`. The intermediate layers could then be reused across multiple projects.

Each layer lives in its own directory containing:
- `layer.conf` - Configuration (ID, name, parent, rootfs size, etc.)
- `layer.sh` - Shell script run in chroot to customize the image

Layers can be stored in `layers/` (tracked in git) or `layers-local/` (ignored by git for private layers).

## Usage

```
Usage: layercake [options] <command> [args]

Commands:
  build [--all] [--force] [--cascade] [layer-id]  Build layer(s)
  list                                            List all layers
  status                                          Show build status
  export <sandfire-data-dir>                      Export to sandfire

Options:
  -layers string        Path to layers directory (default: ./layers)
  -layers-local string  Path to local layers directory (default: ./layers-local)
  -v                    Verbose output
```

## Examples

List all defined layers:
```bash
$ ./layercake list
ID             NAME               PARENT         EXPORT
--             ----               ------         ------
base-ubuntu24  Ubuntu 24.04 Base  scratch
devtools       Development Tools  base-ubuntu24  yes
web            Web Server         base-ubuntu24  yes
```

Check which layers need building:
```bash
$ ./layercake status
ID             STATUS      REASON
--             ------      ------
base-ubuntu24  up-to-date
devtools       stale       parent has been rebuilt
web            stale       parent has been rebuilt
```

Build a specific layer (and any unbuilt parents):
```bash
$ sudo ./layercake build devtools
Layer base-ubuntu24 is up to date
Layer devtools is stale: parent has been rebuilt
Copying rootfs from parent base-ubuntu24...
Mounting rootfs...
Running layer.sh...
[...snipped build output...]
Unmounting rootfs...
Layer devtools built successfully
```

Build all layers:
```bash
sudo ./layercake build --all
```

Force rebuild a layer and all its descendants:
```bash
sudo ./layercake build --cascade base-ubuntu24
```

Export layers marked with `EXPORT=true` to a running Sandfire instance:
```bash
./layercake export /var/lib/sandfire
```

## Layer Configuration

Each layer directory contains a `layer.conf` and a `layer.sh` script. See the included examples:

- Base layer: [layers/base-ubuntu24/](layers/base-ubuntu24/)
- Derived layers: [layers/devtools/](layers/devtools/), [layers/web/](layers/web/)
