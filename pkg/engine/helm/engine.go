/*
Copyright The Helm Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package engine

import (
	"fmt"
	"log"
	"net/netip"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"text/template"

	"github.com/cockroachdb/errors"

	"helm.sh/helm/v3/pkg/chart"
	"helm.sh/helm/v3/pkg/chartutil"
)

// Disks is a package-level lookup table consulted by chart templates that
// reference {{ .Disks }}. It is populated by callers that drive the engine
// (e.g. talm's apply path) before Render is invoked.
//
//nolint:gochecknoglobals // mutable hook into rendering shared across callers; matches upstream lookup wiring.
var Disks = map[string]any{}

// LookupFunc is the package-level implementation of the `lookup` template
// function used when the engine is not in lint mode. It is overridden by
// callers that have a live Kubernetes connection.
//
//nolint:gochecknoglobals // mutable hook into rendering shared across callers; matches upstream lookup wiring.
var LookupFunc = func(string, string, string) (map[string]any, error) {
	return map[string]any{}, nil
}

// Engine is an implementation of the Helm rendering implementation for templates.
type Engine struct {
	// If strict is enabled, template rendering will fail if a template references
	// a value that was not passed in.
	Strict bool
	// In LintMode, some 'required' template values may be missing, so don't fail
	LintMode bool
	// EnableDNS tells the engine to allow DNS lookups when rendering templates
	EnableDNS bool
}

// Render takes a chart, optional values, and value overrides, and attempts to render the Go templates.
//
// Render can be called repeatedly on the same engine.
//
// This will look in the chart's 'templates' data (e.g. the 'templates/' directory)
// and attempt to render the templates there using the values passed in.
//
// Values are scoped to their templates. A dependency template will not have
// access to the values set for its parent. If chart "foo" includes chart "bar",
// "bar" will not have access to the values for "foo".
//
// Values should be prepared with something like `chartutils.ReadValues`.
//
// Values are passed through the templates according to scope. If the top layer
// chart includes the chart foo, which includes the chart bar, the values map
// will be examined for a table called "foo". If "foo" is found in vals,
// that section of the values will be passed into the "foo" chart. And if that
// section contains a value named "bar", that value will be passed on to the
// bar chart during render time.
func (e Engine) Render(chrt *chart.Chart, values chartutil.Values) (map[string]string, error) {
	tmap := allTemplates(chrt, values)

	return e.render(tmap)
}

// Render takes a chart, optional values, and value overrides, and attempts to
// render the Go templates using the default options.
func Render(chrt *chart.Chart, values chartutil.Values) (map[string]string, error) {
	return new(Engine).Render(chrt, values)
}

// renderable is an object that can be rendered.
type renderable struct {
	// tpl is the current template.
	tpl string
	// vals are the values to be supplied to the template.
	vals chartutil.Values
	// namespace prefix to the templates of the current chart
	basePath string
}

const (
	warnStartDelim   = "HELM_ERR_START"
	warnEndDelim     = "HELM_ERR_END"
	recursionMaxNums = 1000

	// Helm template function names registered both in initFunMap
	// and re-injected per render in tplFun for the closure capture.
	helmFuncInclude  = "include"
	helmFuncTpl      = "tpl"
	helmFuncRequired = "required"
	helmFuncLookup   = "lookup"
	helmFuncToToml   = "toToml"
	helmFuncToYAML   = "toYaml"
	helmFuncFromYAML = "fromYaml"
	helmFuncToJSON   = "toJson"

	// helmKeyTalosVersion is the engine-injected template key
	// for the Talos version of the cluster being rendered.
	helmKeyTalosVersion = "TalosVersion"
)

var warnRegex = regexp.MustCompile(warnStartDelim + `((?s).*)` + warnEndDelim)

func warnWrap(warn string) string {
	return warnStartDelim + warn + warnEndDelim
}

// 'include' needs to be defined in the scope of a 'tpl' template as
// well as regular file-loaded templates.
func includeFun(tmpl *template.Template, includedNames map[string]int) func(string, any) (string, error) {
	return func(name string, data any) (string, error) {
		var buf strings.Builder

		if v, ok := includedNames[name]; ok {
			if v > recursionMaxNums {
				return "", errors.Wrapf(errors.New("unable to execute template"), "rendering template has a nested reference name: %s", name)
			}

			includedNames[name]++
		} else {
			includedNames[name] = 1
		}

		err := tmpl.ExecuteTemplate(&buf, name, data)
		includedNames[name]--

		return buf.String(), err
	}
}

// As does 'tpl', so that nested calls to 'tpl' see the templates
// defined by their enclosing contexts.
func tplFun(parent *template.Template, includedNames map[string]int, strict bool) func(string, any) (string, error) {
	return func(tpl string, vals any) (string, error) {
		tmpl, err := parent.Clone()
		if err != nil {
			return "", errors.Wrapf(err, "cannot clone template")
		}

		// Re-inject the missingkey option, see text/template issue https://github.com/golang/go/issues/43022
		// We have to go by strict from our engine configuration, as the option fields are private in Template.
		//nolint:godox // upstream Helm tracks a Go stdlib workaround; comment is intentional.
		// TODO: Remove workaround (and the strict parameter) once we build only with golang versions with a fix.
		if strict {
			tmpl.Option("missingkey=error")
		} else {
			tmpl.Option("missingkey=zero")
		}

		// Re-inject 'include' so that it can close over our clone of tmpl;
		// this lets any 'define's inside tpl be 'include'd.
		tmpl.Funcs(template.FuncMap{
			helmFuncInclude: includeFun(tmpl, includedNames),
			helmFuncTpl:     tplFun(tmpl, includedNames, strict),
		})

		// We need a .New template, as template text which is just blanks
		// or comments after parsing out defines just addes new named
		// template definitions without changing the main template.
		// https://pkg.go.dev/text/template#Template.Parse
		// Use the parent's name for lack of a better way to identify the tpl
		// text string. (Maybe we could use a hash appended to the name?)
		tmpl, err = tmpl.New(parent.Name()).Parse(tpl)
		if err != nil {
			return "", errors.Wrapf(err, "cannot parse template %q", tpl)
		}

		var buf strings.Builder

		err = tmpl.Execute(&buf, vals)
		if err != nil {
			return "", errors.Wrapf(err, "error during tpl function execution for %q", tpl)
		}

		// See comment in renderWithReferences explaining the <no value> hack.
		return strings.ReplaceAll(buf.String(), "<no value>", ""), nil
	}
}

// initFunMap creates the Engine's FuncMap and adds context-specific functions.
func (e Engine) initFunMap(tmpl *template.Template) {
	funcMap := funcMap()
	includedNames := make(map[string]int)

	// Add the template-rendering functions here so we can close over tmpl.
	funcMap[helmFuncInclude] = includeFun(tmpl, includedNames)
	funcMap[helmFuncTpl] = tplFun(tmpl, includedNames, e.Strict)

	// Add the `required` function here so we can use lintMode
	funcMap[helmFuncRequired] = func(warn string, val any) (any, error) {
		// A required value is considered missing when it is nil, or
		// when it is the empty string. Anything else passes through.
		missing := val == nil
		if !missing {
			if str, ok := val.(string); ok && str == "" {
				missing = true
			}
		}

		if !missing {
			return val, nil
		}

		if e.LintMode {
			// Don't fail on missing required values when linting
			log.Printf("[INFO] Missing required value: %s", warn)

			return "", nil
		}

		return val, errors.New(warnWrap(warn))
	}

	// Override sprig fail function for linting and wrapping message
	funcMap["fail"] = func(msg string) (string, error) {
		if e.LintMode {
			// Don't fail when linting
			log.Printf("[INFO] Fail: %s", msg)

			return "", nil
		}

		return "", errors.New(warnWrap(msg))
	}

	// If we are not linting and have a cluster connection, provide a Kubernetes-backed
	// implementation.
	if !e.LintMode {
		funcMap[helmFuncLookup] = LookupFunc
	}

	// When DNS lookups are not enabled override the sprig function and return
	// an empty string.
	if !e.EnableDNS {
		funcMap["getHostByName"] = func(_ string) string {
			return ""
		}
	}

	funcMap["cidrNetwork"] = cidrNetwork
	funcMap["cidrContains"] = cidrContains

	tmpl.Funcs(funcMap)
}

// cidrNetwork returns the network portion of a CIDR (host bits zeroed). The
// canonical "<network>/<prefix>" form is what operators see in Talos docs and
// upstream examples. Sprig ships no equivalent; net/netip's ParsePrefix +
// Masked handles both IPv4 and IPv6 without any host-bit arithmetic in the
// template.
func cidrNetwork(cidr string) (string, error) {
	prefix, err := netip.ParsePrefix(cidr)
	if err != nil {
		return "", fmt.Errorf("cidrNetwork: %w", err)
	}

	return prefix.Masked().String(), nil
}

// cidrContains reports whether the given IP literal falls inside the given
// CIDR. Used by the multi-doc Layer2VIPConfig discovery path to pick the link
// whose subnet hosts the operator-declared floatingIP — net/netip handles
// IPv4 and IPv6 uniformly so chart templates do not have to do per-family bit
// math.
func cidrContains(cidr, addrStr string) (bool, error) {
	prefix, err := netip.ParsePrefix(cidr)
	if err != nil {
		return false, fmt.Errorf("cidrContains: parse cidr %q: %w", cidr, err)
	}

	addr, err := netip.ParseAddr(addrStr)
	if err != nil {
		return false, fmt.Errorf("cidrContains: parse ip %q: %w", addrStr, err)
	}

	return prefix.Contains(addr), nil
}

// render takes a map of templates/values and renders them. The err return is
// named on purpose: the deferred recover below assigns to it.
func (e Engine) render(tpls map[string]renderable) (_ map[string]string, err error) {
	// Basically, what we do here is start with an empty parent template and then
	// build up a list of templates -- one for each file. Once all of the templates
	// have been parsed, we loop through again and execute every template.
	//
	// The idea with this process is to make it possible for more complex templates
	// to share common blocks, but to make the entire thing feel like a file-based
	// template engine.
	defer func() {
		if r := recover(); r != nil {
			err = errors.Errorf("rendering template failed: %v", r)
		}
	}()

	tmpl := template.New("gotpl")
	if e.Strict {
		tmpl.Option("missingkey=error")
	} else {
		// Not that zero will attempt to add default values for types it knows,
		// but will still emit <no value> for others. We mitigate that later.
		tmpl.Option("missingkey=zero")
	}

	e.initFunMap(tmpl)

	// We want to parse the templates in a predictable order. The order favors
	// higher-level (in file system) templates over deeply nested templates.
	keys := sortTemplates(tpls)

	for _, filename := range keys {
		r := tpls[filename]

		_, err := tmpl.New(filename).Parse(r.tpl)
		if err != nil {
			return map[string]string{}, cleanupParseError(filename, err)
		}
	}

	rendered := make(map[string]string, len(keys))
	for _, filename := range keys {
		// Don't render partials. We don't care out the direct output of partials.
		// They are only included from other templates.
		if strings.HasPrefix(path.Base(filename), "_") {
			continue
		}
		// At render time, add information about the template that is being rendered.
		vals := tpls[filename].vals
		vals["Template"] = chartutil.Values{"Name": filename, "BasePath": tpls[filename].basePath}

		var buf strings.Builder

		err := tmpl.ExecuteTemplate(&buf, filename, vals)
		if err != nil {
			return map[string]string{}, cleanupExecError(filename, err)
		}

		// Work around the issue where Go will emit "<no value>" even if Options(missing=zero)
		// is set. Since missing=error will never get here, we do not need to handle
		// the Strict case.
		rendered[filename] = strings.ReplaceAll(buf.String(), "<no value>", "")
	}

	return rendered, nil
}

// errParseTemplate and errExecTemplate are the sentinel errors that
// cleanupParseError and cleanupExecError wrap when reformatting the
// raw text/template diagnostics produced during chart rendering. The
// sentinels exist so err113 (no dynamic errors) is satisfied while
// preserving the exact "<kind> error <in|at> (...)" text the public
// tests assert against.
var (
	errParseTemplate = errors.New("parse error")
	errExecTemplate  = errors.New("execution error")
)

func cleanupParseError(filename string, err error) error {
	tokens := strings.Split(err.Error(), ": ")
	if len(tokens) == 1 {
		// This might happen if a non-templating error occurs.
		return fmt.Errorf("%w in (%s): %w", errParseTemplate, filename, err)
	}
	// The first token is "template"
	// The second token is either "filename:lineno" or "filename:lineNo:columnNo"
	location := tokens[1]
	// The remaining tokens make up a stacktrace-like chain, ending with the relevant error
	errMsg := tokens[len(tokens)-1]

	return fmt.Errorf("%w at (%s): %s", errParseTemplate, location, errMsg)
}

// execErrorTokenCount is the number of colon-separated segments produced by
// text/template's ExecError formatter: "template", location, message.
const execErrorTokenCount = 3

func cleanupExecError(filename string, err error) error {
	var execErr template.ExecError
	if !errors.As(err, &execErr) {
		return err
	}

	tokens := strings.SplitN(err.Error(), ": ", execErrorTokenCount)
	if len(tokens) != execErrorTokenCount {
		// This might happen if a non-templating error occurs.
		return fmt.Errorf("%w in (%s): %w", errExecTemplate, filename, err)
	}

	// The first token is "template"
	// The second token is either "filename:lineno" or "filename:lineNo:columnNo"
	location := tokens[1]

	parts := warnRegex.FindStringSubmatch(tokens[2])
	if len(parts) >= 2 {
		return fmt.Errorf("%w at (%s): %s", errExecTemplate, location, parts[1])
	}

	return err
}

func sortTemplates(tpls map[string]renderable) []string {
	keys := make([]string, len(tpls))

	i := 0
	for key := range tpls {
		keys[i] = key
		i++
	}

	sort.Sort(sort.Reverse(byPathLen(keys)))

	return keys
}

type byPathLen []string

func (p byPathLen) Len() int      { return len(p) }
func (p byPathLen) Swap(i, j int) { p[j], p[i] = p[i], p[j] }
func (p byPathLen) Less(i, j int) bool {
	a, b := p[i], p[j]

	ca, cb := strings.Count(a, "/"), strings.Count(b, "/")
	if ca == cb {
		return a < b
	}

	return ca < cb
}

// allTemplates returns all templates for a chart and its dependencies.
//
// As it goes, it also prepares the values in a scope-sensitive manner.
func allTemplates(c *chart.Chart, vals chartutil.Values) map[string]renderable {
	templates := make(map[string]renderable)
	recAllTpls(c, templates, vals)

	return templates
}

// recAllTpls recurses through the templates in a chart.
//
// As it recurses, it also sets the values to be appropriate for the template
// scope.
func recAllTpls(c *chart.Chart, templates map[string]renderable, vals chartutil.Values) map[string]any {
	subCharts := make(map[string]any)
	chartMetaData := struct {
		chart.Metadata

		IsRoot bool
	}{*c.Metadata, c.IsRoot()}

	next := map[string]any{
		"Chart":             chartMetaData,
		"Files":             newFiles(c.Files),
		"Release":           vals["Release"],
		"Capabilities":      vals["Capabilities"],
		"Values":            make(chartutil.Values),
		"Subcharts":         subCharts,
		"Disks":             Disks,
		helmKeyTalosVersion: vals[helmKeyTalosVersion],
	}

	// If there is a {{.Values.ThisChart}} in the parent metadata,
	// copy that into the {{.Values}} for this template.
	switch {
	case c.IsRoot():
		next["Values"] = vals["Values"]
	default:
		vs, err := vals.Table("Values." + c.Name())
		if err == nil {
			next["Values"] = vs
		}
	}

	for _, child := range c.Dependencies() {
		subCharts[child.Name()] = recAllTpls(child, templates, next)
	}

	newParentID := c.ChartFullPath()
	for _, tplFile := range c.Templates {
		if tplFile == nil {
			continue
		}

		if !isTemplateValid(c, tplFile.Name) {
			continue
		}

		templates[path.Join(newParentID, tplFile.Name)] = renderable{
			tpl:      string(tplFile.Data),
			vals:     next,
			basePath: path.Join(newParentID, "templates"),
		}
	}

	return next
}

// isTemplateValid returns true if the template is valid for the chart type.
func isTemplateValid(ch *chart.Chart, templateName string) bool {
	if isLibraryChart(ch) {
		return strings.HasPrefix(filepath.Base(templateName), "_")
	}

	return true
}

// isLibraryChart returns true if the chart is a library chart.
func isLibraryChart(c *chart.Chart) bool {
	return strings.EqualFold(c.Metadata.Type, "library")
}
