package staticcheck

import (
	"context"
	"fmt"
	"go/ast"
	"go/build"
	"go/token"
	"go/types"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"

	"golang.org/x/tools/go/packages"
)

type typePosition struct {
	path   string
	line   int
	column int
}

type checkedTypes struct {
	direct    *types.Info
	positions map[typePosition]types.Type
	paths     map[string]string
}

type formattingPlatform struct {
	goos   string
	goarch string
	files  []string
}

func (checked checkedTypes) position(fileSet *token.FileSet, expression ast.Expr) typePosition {
	position := fileSet.PositionFor(expression.Pos(), true)
	path := filepath.Clean(position.Filename)
	resolved, known := checked.paths[path]
	if !known {
		resolved = path
		if canonical, err := filepath.EvalSymlinks(path); err == nil {
			resolved = canonical
		}

		checked.paths[path] = resolved
	}

	return typePosition{path: resolved, line: position.Line, column: position.Column}
}

func checkedSourceTypes(fileSet *token.FileSet, information *types.Info) checkedTypes {
	checked := checkedTypes{direct: information, positions: make(map[typePosition]types.Type), paths: make(map[string]string)}
	checked.add(fileSet, information)

	return checked
}

func (checked checkedTypes) add(fileSet *token.FileSet, information *types.Info) {
	for expression, value := range information.Types {
		if value.IsType() {
			checked.positions[checked.position(fileSet, expression)] = value.Type
		}
	}
}

func (checked checkedTypes) callback(fileSet *token.FileSet, expression ast.Expr) (bool, error) {
	if inlineCallback(expression) {
		return true, nil
	}

	switch typed := expression.(type) {
	case *ast.ParenExpr:
		return checked.callback(fileSet, typed.X)
	case *ast.Ellipsis:
		return checked.callback(fileSet, typed.Elt)
	}

	var resolved types.Type
	if checked.direct != nil {
		resolved = checked.direct.TypeOf(expression)
	}

	if resolved == nil {
		resolved = checked.positions[checked.position(fileSet, expression)]
	}

	if resolved == nil {
		return false, fmt.Errorf("type information is unavailable for parameter %s at %s", types.ExprString(expression), fileSet.PositionFor(expression.Pos(), true))
	}

	_, callback := types.Unalias(resolved).Underlying().(*types.Signature)

	return callback, nil
}

func formattingPlatforms(paths []string) ([]formattingPlatform, error) {
	context := build.Default
	context.BuildTags = formattingTags()
	candidates := []formattingPlatform{{goos: context.GOOS, goarch: context.GOARCH, files: nil}}
	var groups []formattingPlatform

	for _, path := range paths {
		matched := false

		for index := 0; index < len(candidates); index++ {
			candidate := candidates[index]
			context.GOOS, context.GOARCH = candidate.goos, candidate.goarch
			matches, err := context.MatchFile(filepath.Dir(path), filepath.Base(path))
			if err != nil {
				return nil, fmt.Errorf("select argument formatting platform for %s: %w", path, err)
			}

			if matches {
				groups = addFormattingPlatform(groups, candidate, path)
				matched = true
				break
			}

			if len(candidates) == 1 {
				available, err := otherFormattingPlatforms()
				if err != nil {
					return nil, err
				}

				candidates = append(candidates, available...)
			}
		}

		if !matched {
			action := "enable its build tags in GOFLAGS"
			if _, fromMake := os.LookupEnv("GO_MK_FORMAT_BUILD_TAGS"); fromMake {
				action = "set run.build-tags to enable this file"
			}

			return nil, fmt.Errorf("selected source %s does not match a supported Go platform; %s", path, action)
		}
	}

	return groups, nil
}

func formattingBuildTags(flags string) []string {
	words := strings.Fields(flags)
	var tags []string

	for index := 0; index < len(words); index++ {
		word := words[index]
		if word == "-tags" && index+1 < len(words) {
			index++
			tags = strings.Split(words[index], ",")
		} else if values, found := strings.CutPrefix(word, "-tags="); found {
			tags = strings.Split(values, ",")
		}
	}

	return tags
}

func formattingTags() []string {
	tags := formattingBuildTags(os.Getenv("GOFLAGS"))
	additional := strings.FieldsFunc(os.Getenv("GO_MK_FORMAT_BUILD_TAGS"), func(character rune) bool {
		return character == ',' || character == ' '
	})

	return append(tags, additional...)
}

func otherFormattingPlatforms() ([]formattingPlatform, error) {
	output, err := exec.CommandContext(context.Background(), "go", "tool", "dist", "list").CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("list argument formatting platforms: %w\n%s", err, output)
	}

	var platforms []formattingPlatform

	for target := range strings.FieldsSeq(string(output)) {
		goos, goarch, valid := strings.Cut(target, "/")
		if !valid {
			return nil, fmt.Errorf("invalid Go platform %q", target)
		}

		platforms = append(platforms, formattingPlatform{goos: goos, goarch: goarch, files: nil})
	}

	return platforms, nil
}

func addFormattingPlatform(groups []formattingPlatform, candidate formattingPlatform, path string) []formattingPlatform {
	for index := range groups {
		if groups[index].goos == candidate.goos && groups[index].goarch == candidate.goarch {
			groups[index].files = append(groups[index].files, path)
			return groups
		}
	}

	candidate.files = []string{path}

	return append(groups, candidate)
}

func loadFormattingTypes(groups []formattingPlatform, overlay map[string][]byte) (map[string]checkedTypes, error) {
	checked := make(map[string]checkedTypes)

	for _, group := range groups {
		selected := make(map[string]bool)
		patterns := make(map[string]bool)

		for _, path := range group.files {
			selected[path] = true
			patterns[filepath.Dir(path)] = true
		}

		ordered := make([]string, 0, len(patterns))
		for pattern := range patterns {
			ordered = append(ordered, pattern)
		}

		sort.Strings(ordered)
		configuration := &packages.Config{Mode: packages.LoadSyntax, Tests: true, Overlay: overlay, Dir: filepath.Dir(group.files[0])}
		configuration.Env = append(os.Environ(), "GOOS="+group.goos, "GOARCH="+group.goarch)
		if (group.goos != build.Default.GOOS || group.goarch != build.Default.GOARCH) && strings.TrimSpace(os.Getenv("GO_MK_CC")) == "" {
			configuration.Env = append(configuration.Env, "CGO_ENABLED=0")
		}

		if tags := formattingTags(); len(tags) > 0 {
			configuration.BuildFlags = []string{"-tags=" + strings.Join(tags, ",")}
		}

		loaded, err := packages.Load(configuration, ordered...)
		if err != nil {
			return nil, fmt.Errorf("load argument formatting types for %s/%s: %w", group.goos, group.goarch, err)
		}

		information := checkedTypes{direct: nil, positions: make(map[typePosition]types.Type), paths: make(map[string]string)}

		for _, pkg := range loaded {
			if len(pkg.Errors) > 0 {
				return nil, fmt.Errorf("load argument formatting package %s for %s/%s: %w", pkg.PkgPath, group.goos, group.goarch, pkg.Errors[0])
			}

			if pkg.TypesInfo == nil {
				return nil, fmt.Errorf("argument formatting package %s lacks type information", pkg.PkgPath)
			}

			information.add(pkg.Fset, pkg.TypesInfo)
		}

		for path := range selected {
			checked[path] = information
		}
	}

	return checked, nil
}
