package tui

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// detectProject inspects dir and returns a short human-readable string
// describing the project type and key metadata (module name, version, etc.).
// Returns an empty string if nothing identifiable is found.
func detectProject(dir string) string {
	if dir == "" {
		return ""
	}

	var parts []string

	// Go module
	if data, err := os.ReadFile(filepath.Join(dir, "go.mod")); err == nil {
		module, goVer := parseGoMod(string(data))
		if module != "" {
			parts = append(parts, fmt.Sprintf("Go module: %s", module))
		}
		if goVer != "" {
			parts = append(parts, fmt.Sprintf("Go version: %s", goVer))
		}
	}

	// Node.js / npm / yarn
	if data, err := os.ReadFile(filepath.Join(dir, "package.json")); err == nil {
		name, version, pkgType := parsePackageJSON(data)
		label := pkgType
		if name != "" {
			label += fmt.Sprintf(": %s", name)
		}
		if version != "" {
			label += fmt.Sprintf(" v%s", version)
		}
		parts = append(parts, label)
	}

	// Rust
	if data, err := os.ReadFile(filepath.Join(dir, "Cargo.toml")); err == nil {
		name, version := parseCargoToml(string(data))
		label := "Rust crate"
		if name != "" {
			label += fmt.Sprintf(": %s", name)
		}
		if version != "" {
			label += fmt.Sprintf(" v%s", version)
		}
		parts = append(parts, label)
	}

	// Python
	if _, err := os.Stat(filepath.Join(dir, "pyproject.toml")); err == nil {
		parts = append(parts, "Python project (pyproject.toml)")
	} else if _, err := os.Stat(filepath.Join(dir, "setup.py")); err == nil {
		parts = append(parts, "Python project (setup.py)")
	} else if _, err := os.Stat(filepath.Join(dir, "requirements.txt")); err == nil {
		parts = append(parts, "Python project (requirements.txt)")
	}

	// Java / Maven
	if _, err := os.Stat(filepath.Join(dir, "pom.xml")); err == nil {
		parts = append(parts, "Java/Maven project")
	}

	// Java / Gradle
	if _, err := os.Stat(filepath.Join(dir, "build.gradle")); err == nil || func() bool {
		_, e := os.Stat(filepath.Join(dir, "build.gradle.kts")); return e == nil
	}() {
		parts = append(parts, "Java/Gradle project")
	}

	// .NET
	if matches, _ := filepath.Glob(filepath.Join(dir, "*.csproj")); len(matches) > 0 {
		parts = append(parts, fmt.Sprintf(".NET project (%s)", filepath.Base(matches[0])))
	}

	if len(parts) == 0 {
		return ""
	}

	// Git branch
	if branch := gitBranch(dir); branch != "" {
		parts = append(parts, fmt.Sprintf("git branch: %s", branch))
	}

	return strings.Join(parts, " · ")
}

func parseGoMod(content string) (module, goVersion string) {
	scanner := bufio.NewScanner(strings.NewReader(content))
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if strings.HasPrefix(line, "module ") {
			module = strings.TrimPrefix(line, "module ")
		} else if strings.HasPrefix(line, "go ") {
			goVersion = strings.TrimPrefix(line, "go ")
		}
		if module != "" && goVersion != "" {
			break
		}
	}
	return
}

func parsePackageJSON(data []byte) (name, version, pkgType string) {
	var pkg struct {
		Name    string `json:"name"`
		Version string `json:"version"`
		Type    string `json:"type"` // "module" or "commonjs"
		Scripts map[string]string `json:"scripts"`
	}
	if err := json.Unmarshal(data, &pkg); err != nil {
		return "", "", "Node.js project"
	}
	name = pkg.Name
	version = pkg.Version
	if _, ok := pkg.Scripts["next"]; ok {
		pkgType = "Next.js project"
	} else if _, ok := pkg.Scripts["react-scripts"]; ok {
		pkgType = "React project"
	} else if _, ok := pkg.Scripts["vite"]; ok {
		pkgType = "Vite project"
	} else {
		pkgType = "Node.js project"
	}
	return
}

func parseCargoToml(content string) (name, version string) {
	inPkg := false
	scanner := bufio.NewScanner(strings.NewReader(content))
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "[package]" {
			inPkg = true
			continue
		}
		if strings.HasPrefix(line, "[") {
			inPkg = false
		}
		if !inPkg {
			continue
		}
		if strings.HasPrefix(line, "name") {
			parts := strings.SplitN(line, "=", 2)
			if len(parts) == 2 {
				name = strings.Trim(strings.TrimSpace(parts[1]), `"`)
			}
		}
		if strings.HasPrefix(line, "version") {
			parts := strings.SplitN(line, "=", 2)
			if len(parts) == 2 {
				version = strings.Trim(strings.TrimSpace(parts[1]), `"`)
			}
		}
	}
	return
}

func gitBranch(dir string) string {
	headPath := filepath.Join(dir, ".git", "HEAD")
	data, err := os.ReadFile(headPath)
	if err != nil {
		return ""
	}
	line := strings.TrimSpace(string(data))
	if strings.HasPrefix(line, "ref: refs/heads/") {
		return strings.TrimPrefix(line, "ref: refs/heads/")
	}
	return ""
}
