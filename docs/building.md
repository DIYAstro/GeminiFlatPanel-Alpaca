# Compiling from Source

[← Back to main readme](../readme.md)

## Prerequisites
* **Go:** 1.24+ installed (see `go.mod` for the exact minimum).
* **Node.js:** 18+ and `npm` installed (to compile the frontend).

## Steps
1. Clone the repository.
2. Build the Windows executable and installer by running:
   ```cmd
   build_scripts\build_installer.bat
   ```
   Or build the Windows executable directly without the installer:
   ```cmd
   build_scripts\build_exe.bat
   ```
3. Compiled files will be created in the `build/` directory.
