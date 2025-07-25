# UI Development Guide

This guide explains how to set up the development environment for the new `templ` and React-based UI, how to run the development tools, and how the build process works.

## 1. Initial Setup (Phase 0)

Before you begin, ensure you have the following installed:
*   Go (version 1.22 or higher)
*   Node.js and npm (or your preferred package manager)
*   `make` (optional, for using the Makefile shortcuts)

The initial setup involves these key steps:

1.  **Go Toolchain:** The `go.mod` file is configured to use the Go 1.22 toolchain.
2.  **Install `templ`:** The `templ` CLI is required for generating Go code from `.templ` files. Install it globally:
    ```bash
    go install github.com/a-h/templ/cmd/templ@latest
    ```
3.  **Setup Vite + React:** Navigate to the `/ui` directory and install the Node.js dependencies:
    ```bash
    cd ui
    npm install
    ```

## 2. Development Workflow

The development process involves two separate but cooperating toolchains.

### Generating HTML with `templ`

All `.templ` files in the `/templates` directory are Go functions that generate HTML. After making any changes to a `.templ` file, you must run the `templ` generator to produce the corresponding `.templ.go` file.

*   **To generate once:**
    ```bash
    templ generate ./...
    ```
*   **To watch for changes and regenerate automatically:**
    ```bash
    templ generate --watch
    ```
    The `make build` command also includes this generation step.

### Developing React Islands with Vite

The interactive components are located in `/ui`. Vite provides a hot-reloading development server for a fast feedback loop.

*   **Start the Vite dev server:**
    ```bash
    cd ui
    npm run dev
    ```
*   **Building for Production:** When you are done with development, you need to build the final JavaScript and CSS assets.
    ```bash
    cd ui
    npm run build
    ```
    This command, managed by Vite, will output the bundled and minified assets into the `/internal/assets/static/` directory, ready to be embedded into the Go binary.

## 3. Build and CI Process (Phase 5)

The full build and release process is automated to ensure consistency.

1.  **Asset Embedding:** The production assets generated by `vite build` (e.g., `react-islands.js`, `report.css`) are placed in `/internal/assets/static/` and embedded directly into the Go binary using `//go:embed`. This makes the final `adlercov` executable completely self-contained.
2.  **Go Build:** The `make build` or `go build ./...` command will first run `templ generate` and then compile the Go application, including the embedded assets.
3.  **GitHub Actions:** The CI pipeline is configured to automate these steps:
    *   Cache Node.js modules for faster builds.
    *   Run `npm ci && npm run build` to prepare the UI assets.
    *   Run `templ generate` to update Go templates.
    *   Run `go test ./...` to ensure all tests pass.
    *   Build the final release artifacts.
4.  **Performance Budgets:** The CI pipeline includes a step to check the size of the final JavaScript and CSS assets to prevent accidental bloat and maintain fast load times.

## 4. Accepting Contributions

*   **For Go/`templ` changes:** Please run `templ fmt ./...` and `templ generate` before committing your changes.
*   **For React/UI changes:** Please run `npm run build` and commit the updated assets in `/internal/assets/static/`. This ensures that contributors who only work on the Go parts do not need to have Node.js installed.