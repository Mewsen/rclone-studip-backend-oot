# Stud.IP Backend for rclone

This project provides an rclone backend for accessing files from Stud.IP instances.

## Usage

This backend can be used in two ways:

- as a standalone rclone-compatible binary (recommended)

or 

- as an rclone plugin loaded by an existing rclone installation


### AUR Package

```bash
paru rclone-studip-git 
```

### Standalone binary

Download the binary for your platform from the [releases page](https://github.com/Mewsen/rclone-studip-backend-oot/releases).

It is recommended to move the binary somewhere in your `PATH`.

Then continue with the [configuration](#configuration).

### Plugin with existing rclone

To use the plugin artifact with an existing rclone binary:

```bash
cp build/librcloneplugin_backend_studip.so "$RCLONE_PLUGIN_PATH/"
```

Then continue with the [configuration](#configuration).

Notes:

- Linux and MacOS only.
- All plugins in `$RCLONE_PLUGIN_PATH` are loaded.
- If `RCLONE_PLUGIN_PATH` is not set, plugin support is disabled.
- Plugin and rclone must be built from compatible source versions.

## Configuration

Create a remote with `rclone config`, then choose storage type `Stud.IP`.

Backend options:

- `base_url`: Base URL of the Stud.IP JSON API v1 endpoint.  
  Example: `https://elearning.uni-bremen.de/jsonapi.php/v1/`
- `username`: Stud.IP login username.
- `password`: Stud.IP login password (stored obscured by rclone config).
- `course_id`: Stud.IP course ID.
  Example: `59e88658b39093836455413bd1f24f29`
- `license`: License ID applied to uploaded files. Default: `UNDEF_LICENSE`.

Supported `license` values:

- `FREE_LICENSE`
- `SELFMADE_NONPUB`
- `NON_TEXTUAL`
- `TEXT_NO_LICENSE`
- `WITH_LICENSE`
- `UNDEF_LICENSE`

Important:

- If `UNDEF_LICENSE` is used, uploaded files are not readable until a valid license is chosen.

## Usage examples

```bash
# List directories / files at remote root
rclone lsd studip-bremen-ma1:
rclone ls studip-bremen-ma1:
rclone lsf studip-bremen-ma1:

# Create a directory in the configured course
rclone mkdir studip-bremen-ma1:uploads

# Upload a directory recursively
rclone copy ./localdir studip-bremen-ma1:uploads/localdir

# Upload a single file to an exact destination path
rclone copyto ./report.pdf studip-bremen-ma1:uploads/report.pdf

# Stream text into a remote file
printf "hello from rclone\n" | rclone rcat studip-bremen-ma1:uploads/hello.txt

# Download files back to local disk
rclone copy studip-bremen-ma1:uploads ./downloads/uploads

# Show file content
rclone cat studip-bremen-ma1:uploads/hello.txt

# Remove files / empty directories
rclone deletefile studip-bremen-ma1:uploads/hello.txt
rclone rmdir studip-bremen-ma1:uploads/empty-dir

rclone mount studip-bremen-ma1: ./mount
```

## Build

1. Install Mage (one-time):

```bash
go install github.com/magefile/mage@latest
```

2. Build plugin and standalone binaries:

```bash
mage
```

Build output:

- `build/rclone-studip-darwin-amd64`
- `build/rclone-studip-darwin-arm64`
- `build/rclone-studip-linux-amd64`
- `build/rclone-studip-linux-arm64`
- `build/rclone-studip-windows-amd64.exe`
- `build/rclone-studip-windows-arm64.exe`
- `build/librcloneplugin_backend_studip.so`
