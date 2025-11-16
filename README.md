# Stud.IP Backend for rclone

This project provides an **rclone backend** for accessing files from **Stud.IP** instances.

Although it should work with all Stud.IP instances, it has currently only been tested with the [University of Bremen](https://elearning.uni-bremen.de) installation.

**Note:** This is a **proof of concept** and currently offers **limited functionality (read-only access)** and **may contain bugs**.

## Usage

You can use this backend either as an rclone plugin or by building a custom rclone binary with the backend compiled in.

### 1. Build as an rclone plugin (Supported on macOS & Linux as of now)

1. Edit `/backend/studip/studip.go` and change the package declaration at the top from:

```go
package studip
````

to:

```go
package main
```

2. Build the plugin using Go’s plugin build mode:

```bash
go build --buildmode=plugin -o librcloneplugin_backend_studip.so backend/studip/studip.go
```

3. Load the plugin

```bash
mv librcloneplugin_backend_studip.so $RCLONE_PLUGIN_PATH/
```
- All plugins in the folder specified by variable $RCLONE_PLUGIN_PATH are loaded.
- If this variable doesn't exist, plugin support is disabled.
- Plugins must be compiled against the exact version of rclone to work. (The rclone used during building the plugin must be the same as the source of rclone)




### 2. Build a custom rclone binary with the backend included

You can also build a full rclone binary that has the Stud.IP backend compiled in.

From the repository root, build rclone with:

```bash
go build -o rclone-studip
```
