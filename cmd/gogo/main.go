// Command gogo is a superset of Go. It pre-processes .gogo files — applying
// gogo language extensions — and feeds the resulting source directly to the
// standard Go toolchain. All existing .go syntax is accepted unchanged, making
// gogo fully backwards-compatible.
//
// # Usage
//
//	gogo run   [goflags] <file.gogo> [--] [program args]
//	gogo build [goflags] [packages]
//	gogo test  [goflags] [packages]
//	gogo <any go sub-command> [args...]   — passed through unchanged
//
// # Union types
//
// gogo adds union type declarations using '=' and '|':
//
//	type Number = int | float64          // any number of alternatives
//	type Integer = ~int | ~int8 | ~int16 // approximation elements work too
//
// These are converted to Go 1.18+ interface type constraints:
//
//	type Number interface{ int | float64 }
//
// Union types can then be used as generic type constraints:
//
//	func Sum[T Number](a, b T) T { return a + b }
package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/benbenbenbenbenben/gogo/internal/preprocessor"
	"github.com/benbenbenbenbenben/gogo/internal/sourcegen"
)

// gogoExt is the file extension recognised by gogo.
const gogoExt = sourcegen.SourceExt

func main() {
	args := os.Args[1:]
	if len(args) == 0 {
		printUsage()
		os.Exit(1)
	}

	switch args[0] {
	case "run":
		cmdRun(args[1:])
	case "build":
		cmdBuildOrTest("build", args[1:])
	case "test":
		cmdBuildOrTest("test", args[1:])
	case "preprocess":
		cmdPreprocess(args[1:])
	default:
		// Unknown sub-commands are forwarded to the Go toolchain as-is.
		runGo(args)
	}
}

// cmdRun handles: gogo run [goflags] <file.gogo> [-- program args]
func cmdRun(args []string) {
	// Split the argument list into:
	//   goFlags  — flags that belong to "go run" (start with '-')
	//   gogoFile — the first .gogo file found
	//   rest     — everything after the .gogo file (program arguments)
	var goFlags []string
	var gogoFile string
	var rest []string

	for i, arg := range args {
		if gogoFile == "" {
			if strings.HasSuffix(arg, gogoExt) {
				gogoFile = arg
				rest = args[i+1:]
				break
			}
			goFlags = append(goFlags, arg)
		}
	}

	if gogoFile == "" {
		// No .gogo file — pass straight through to go run.
		runGo(append([]string{"run"}, args...))
		return
	}

	// Preprocess the .gogo file into a temporary directory.
	tmpDir, err := os.MkdirTemp("", "gogo-run-*")
	if err != nil {
		fatalf("cannot create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	tmpFile, err := preprocessToDir(gogoFile, tmpDir)
	if err != nil {
		fatalf("%v", err)
	}

	// Build and run: go run [goFlags] <tmpFile> [rest...]
	cmdArgs := []string{"run"}
	cmdArgs = append(cmdArgs, goFlags...)
	cmdArgs = append(cmdArgs, tmpFile)
	cmdArgs = append(cmdArgs, rest...)
	runGo(cmdArgs)
}

// cmdBuildOrTest handles: gogo build|test [goflags] [packages]
//
// For each directory that contains .gogo files it generates a _gogo_gen.go
// file, runs the Go sub-command, then cleans up the generated files.
func cmdBuildOrTest(subcommand string, args []string) {
	dirs := dirsFromArgs(args)
	generated, err := generateAll(dirs)
	if err != nil {
		fatalf("%v", err)
	}
	defer removeAll(generated)

	runGo(append([]string{subcommand}, args...))
}

// cmdPreprocess handles: gogo preprocess <file.gogo>
//
// It writes the preprocessed Go source to stdout — useful for debugging.
func cmdPreprocess(args []string) {
	if len(args) == 0 {
		fatalf("preprocess: no file specified")
	}
	for _, arg := range args {
		src, err := os.ReadFile(arg)
		if err != nil {
			fatalf("preprocess: %v", err)
		}
		fmt.Print(preprocessor.Process(string(src)))
	}
}

// generateAll preprocesses every .gogo file found in dirs, writing a
// corresponding _gogo_gen.go file.  It returns the list of generated paths so
// the caller can remove them when done.
func generateAll(dirs []string) ([]string, error) {
	var generated []string
	for _, dir := range dirs {
		matches, err := filepath.Glob(filepath.Join(dir, "*"+gogoExt))
		if err != nil {
			return generated, fmt.Errorf("glob %s: %w", dir, err)
		}
		for _, gf := range matches {
			out, err := preprocessToSibling(gf)
			if err != nil {
				return generated, err
			}
			generated = append(generated, out)
		}
	}
	return generated, nil
}

// preprocessToDir preprocesses src into outDir and returns the path of the
// generated .go file.
func preprocessToDir(src, outDir string) (string, error) {
	return sourcegen.WritePreprocessedToDir(src, outDir)
}

// preprocessToSibling generates a _gogo_gen.go file next to the .gogo source
// file and returns its path.
func preprocessToSibling(gogoFile string) (string, error) {
	return sourcegen.WriteGeneratedSibling(gogoFile)
}

// dirsFromArgs extracts directories from a go-style argument list.
// If no directories are found the current directory is assumed.
func dirsFromArgs(args []string) []string {
	var dirs []string
	for _, arg := range args {
		if strings.HasPrefix(arg, "-") {
			continue
		}
		switch arg {
		case "./...":
			dirs = append(dirs, walkDirs(".")...)
		default:
			if strings.HasSuffix(arg, "/...") {
				root := strings.TrimSuffix(arg, "/...")
				dirs = append(dirs, walkDirs(root)...)
			} else if info, err := os.Stat(arg); err == nil && info.IsDir() {
				dirs = append(dirs, arg)
			}
		}
	}
	if len(dirs) == 0 {
		dirs = []string{"."}
	}
	return dirs
}

// walkDirs returns all directories rooted at root.
func walkDirs(root string) []string {
	var dirs []string
	_ = filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil
		}
		if info.IsDir() {
			dirs = append(dirs, path)
		}
		return nil
	})
	return dirs
}

// removeAll deletes every file in the list, ignoring errors.
func removeAll(files []string) {
	for _, f := range files {
		os.Remove(f) //nolint:errcheck
	}
}

// runGo executes the go binary with args, wiring stdio to the current process.
// It exits with the same exit code as the child process.
func runGo(args []string) {
	cmd := exec.Command("go", args...)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	if err := cmd.Run(); err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			os.Exit(exitErr.ExitCode())
		}
		fatalf("go: %v", err)
	}
}

func fatalf(format string, a ...any) {
	fmt.Fprintf(os.Stderr, "gogo: "+format+"\n", a...)
	os.Exit(1)
}

func printUsage() {
	fmt.Fprintf(os.Stderr, `gogo — a superset of Go

Usage:
  gogo run   [goflags] <file.gogo> [-- program args]
  gogo build [goflags] [packages]
  gogo test  [goflags] [packages]
  gogo preprocess <file.gogo>         print preprocessed Go to stdout
  gogo <go command> [args...]         passed through to the Go toolchain

Union type syntax:
  type Name = TypeA | TypeB | TypeC
  → type Name interface{ TypeA | TypeB | TypeC }
`)
}
