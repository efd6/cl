// cl runs the capslock tool on all imported packages from a module or
// set of packages within a module.
package main

import (
	"bytes"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strings"

	"golang.org/x/sys/execabs"
	"golang.org/x/tools/go/packages"
)

// Exit status codes.
const (
	success       = 0
	internalError = 1 << (iota - 1)
	invocationError
	capChangeError // capChangeError is the status code for a caps change.
)

func main() {
	os.Exit(Main())
}

func Main() int {
	lock := flag.Bool("lock", false, "write out a new lock file")
	module := flag.Bool("mod", true, "include the whole main module")
	list := flag.Bool("imports", false, "list imports that would be analysed and then exit")
	stdlib := flag.Bool("stdlib", false, "include stdlib packages in analysis")
	verbose := flag.Bool("v", false, "print verbose output")
	goos := flag.String("goos", "", "GOOS to use for analysis")
	goarch := flag.String("goarch", "", "GOARCH to use for analysis")
	custom := flag.String("capability_map", "", "use a custom capability map file")
	noBuiltin := flag.Bool("disable_builtin", false, "disable the builtin capability mappings when using a custom capability map")
	ignore := make(set)
	flag.Var(ignore, "i", "imported package path patterns to ignore (allows multiple instances)")
	flag.Parse()
	ignorer, err := ignore.regexps()
	if *noBuiltin && *custom == "" {
		fmt.Fprintln(os.Stderr, "disable_builtin requires capability_map")
		return invocationError
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return invocationError
	}
	if *goos == "" {
		*goos = runtime.GOOS
	}
	if *goarch == "" {
		*goarch = runtime.GOARCH
	}
	return analyse(*goos, *goarch, ignorer, *module, *list, *lock, *stdlib, *verbose, *noBuiltin, *custom)
}

type set map[string]bool

func (s set) Set(v string) error {
	s[v] = true
	return nil
}

func (s set) String() string {
	p := make([]string, 0, len(s))
	for y := range s {
		p = append(p, y)
	}
	sort.Strings(p)
	return strings.Join(p, ",")
}

func (s set) regexps() ([]*regexp.Regexp, error) {
	re := make([]*regexp.Regexp, 0, len(s))
	for p := range s {
		r, err := regexp.Compile(p)
		if err != nil {
			return nil, err
		}
		re = append(re, r)
	}
	return re, nil
}

type matchers []*regexp.Regexp

func (m matchers) match(s string) bool {
	for _, re := range m {
		if re.MatchString(s) {
			return true
		}
	}
	return false
}

func analyse(goos, goarch string, ignore matchers, module, list, lock, stdlib, verbose, noBuiltin bool, custom string) int {
	root, valid, err := moduleRoot()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		if valid {
			return invocationError
		}
		return internalError
	}
	if !module {
		root, err = os.Getwd()
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			return internalError
		}
	}

	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return internalError
	}

	cfg := &packages.Config{
		Tests: false,
		Mode:  packages.NeedImports | packages.NeedModule,
		Env: append(os.Environ(),
			"GOOS="+goos,
			"GOARCH="+goarch,
		),
	}
	pkgs, err := packages.Load(cfg, filepath.Join(root, "..."))
	if err != nil {
		fmt.Fprintf(os.Stderr, "load: %v\n", err)
		return internalError
	}
	if packages.PrintErrors(pkgs) != 0 {
		return internalError
	}

	imps := make(map[string][]string)
	for _, pkg := range pkgs {
		for imp := range pkg.Imports {
			if strings.HasPrefix(imp, pkg.Module.Path) {
				continue
			}
			if ignore.match(imp) {
				continue
			}
			imps[imp] = append(imps[imp], pkg.String())
		}
	}
	imports := make([]string, 0, len(imps))
	for i, by := range imps {
		if !stdlib {
			isStd, err := isStdlib(i, goos, goarch)
			if err != nil {
				fmt.Fprintf(os.Stderr, "%v: imported by %s\n", err, strings.Join(by, ","))
				return internalError
			}
			if isStd {
				continue
			}
		}
		imports = append(imports, i)
	}
	if list {
		sort.Strings(imports)
		for _, i := range imports {
			fmt.Println(i)
		}
		return success
	}
	if lock {
		buf, err := capslock(goos, goarch, imports, "verbose", filepath.Join(root, "caps.summary"), custom, noBuiltin)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			return internalError
		}
		if verbose {
			fmt.Println(buf)
		}
		_, err = capslock(goos, goarch, imports, "json", filepath.Join(root, "caps.lock"), custom, noBuiltin)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			return internalError
		}
	} else {
		buf, err := capslock(goos, goarch, imports, "compare", filepath.Join(root, "caps.lock"), custom, noBuiltin)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			return internalError
		}
		fmt.Print(buf)
		if buf.Len() != 0 {
			return capChangeError
		}
	}
	return success
}

// moduleRoot returns the root directory of the module in the current dir and
// whether a go.mod file can be found. It returns an error if the go tool is
// not running in module-aware mode.
func moduleRoot() (root string, valid bool, err error) {
	cmd := execabs.Command("go", "env", "GOMOD")
	var buf, errBuf bytes.Buffer
	cmd.Stdout = &buf
	cmd.Stderr = &errBuf
	err = cmd.Run()
	if err != nil {
		return "", false, fmt.Errorf("go env %w: %v", err, &errBuf)
	}
	gomod := strings.TrimSpace(buf.String())
	if gomod == "" {
		return "", true, errors.New("go tool not running in module mode")
	}
	if gomod == os.DevNull {
		return "", true, errors.New("no go.mod")
	}
	return filepath.Dir(gomod), true, nil
}

// isStdlibeturns whether p is a standard library package path.
func isStdlib(p, goos, goarch string) (ok bool, err error) {
	cmd := execabs.Command("go", "list", "-f={{.Standard}}", p)
	cmd.Env = append(os.Environ(),
		"GOOS="+goos,
		"GOARCH="+goarch,
	)
	var buf, errBuf bytes.Buffer
	cmd.Stdout = &buf
	cmd.Stderr = &errBuf
	err = cmd.Run()
	if err != nil {
		note, _, ok := strings.Cut(errBuf.String(), ";")
		if ok {
			return false, fmt.Errorf("go list %w: %s", err, note)
		}
		return false, fmt.Errorf("go list %w: %s", err, &errBuf)
	}
	return strings.TrimSpace(buf.String()) == "true", nil
}

// capslock runs the capslock tool with the provided GOOS and GOARCH on pkgs.
// If format is json or verbose, the output is written to a file at path. If
// format is compare, the contents of the file at path are used as the
// baseline for comparison.
func capslock(goos, goarch string, pkgs []string, format, path, custom string, noBuiltin bool) (*bytes.Buffer, error) {
	args := []string{"-goos", goos, "-goarch", goarch, "-output", format, "-packages", strings.Join(pkgs, ",")}
	if format == "compare" {
		args = append(args, path)
	}
	if custom != "" {
		args = append(args, "capability_map", custom)
		if noBuiltin {
			args = append(args, "disable_builtin")
		}
	}
	cmd := execabs.Command("capslock", args...)
	var buf, errBuf bytes.Buffer
	cmd.Stdout = &buf
	cmd.Stderr = &errBuf
	err := cmd.Run()
	if err != nil {
		return nil, fmt.Errorf("capslock: %w: %v", err, &errBuf)
	}
	if format != "compare" {
		err = os.WriteFile(path, buf.Bytes(), 0o664)
	}
	return &buf, err
}
