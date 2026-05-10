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
	"path"
	"strings"
	"sync"
	"testing"
	"text/template"

	"github.com/cockroachdb/errors"

	"helm.sh/helm/v3/pkg/chart"
	"helm.sh/helm/v3/pkg/chartutil"
)

func TestSortTemplates(t *testing.T) {
	tpls := map[string]renderable{
		tplFooDirect:        {},
		tplFooNested:        {},
		tplBarDirect:        {},
		tplBarSubChartFoo:   {},
		tplFooHelpersDirect: {},
		tplFooSubChartFoo:   {},
		tplFooSubChartBar:   {},
	}
	got := sortTemplates(tpls)
	if len(got) != len(tpls) {
		t.Fatal("Sorted results are missing templates")
	}

	expect := []string{
		tplFooNested,
		tplFooSubChartFoo,
		tplBarSubChartFoo,
		tplFooSubChartBar,
		tplFooDirect,
		tplBarDirect,
		tplFooHelpersDirect,
	}
	for i, e := range expect {
		if got[i] != e {
			t.Fatalf("\n\tExp:\n%s\n\tGot:\n%s",
				strings.Join(expect, "\n"),
				strings.Join(got, "\n"),
			)
		}
	}
}

func TestFuncMap(t *testing.T) {
	fns := funcMap()
	forbidden := []string{"env", "expandenv"}
	for _, f := range forbidden {
		if _, ok := fns[f]; ok {
			t.Errorf("Forbidden function %s exists in FuncMap.", f)
		}
	}

	// Test for Engine-specific template functions.
	expect := []string{"include", "required", "tpl", "toYaml", "fromYaml", "toToml", "toJson", helmFuncFromJSON, "lookup"}
	for _, f := range expect {
		if _, ok := fns[f]; !ok {
			t.Errorf("Expected add-on function %q", f)
		}
	}
}

func TestRender(t *testing.T) {
	c := &chart.Chart{
		Metadata: &chart.Metadata{
			Name:    "moby",
			Version: helmTestVersion,
		},
		Templates: []*chart.File{
			{Name: "templates/test1", Data: []byte("{{.Values.outer | title }} {{.Values.inner | title}}")},
			{Name: "templates/test2", Data: []byte("{{.Values.global.callme | lower }}")},
			{Name: "templates/test3", Data: []byte("{{.noValue}}")},
			{Name: "templates/test4", Data: []byte("{{toJson .Values}}")},
			{Name: "templates/test5", Data: []byte("{{getHostByName \"helm.sh\"}}")},
		},
		Values: map[string]any{"outer": helmTestDefaultValue, "inner": helmTestDefaultValue},
	}

	vals := map[string]any{
		helmKeyValues: map[string]any{
			"outer": "spouter",
			"inner": "inn",
			"global": map[string]any{
				"callme": "Ishmael",
			},
		},
	}

	v, err := chartutil.CoalesceValues(c, vals)
	if err != nil {
		t.Fatalf("Failed to coalesce values: %s", err)
	}
	out, err := Render(c, v)
	if err != nil {
		t.Errorf("Failed to render templates: %s", err)
	}

	expect := map[string]string{
		"moby/templates/test1": "Spouter Inn",
		"moby/templates/test2": "ishmael",
		"moby/templates/test3": "",
		"moby/templates/test4": `{"global":{"callme":"Ishmael"},"inner":"inn","outer":"spouter"}`,
		"moby/templates/test5": "",
	}

	for name, data := range expect {
		if out[name] != data {
			t.Errorf("Expected %q, got %q", data, out[name])
		}
	}
}

func TestRenderRefsOrdering(t *testing.T) {
	parentChart := &chart.Chart{
		Metadata: &chart.Metadata{
			Name:    "parent",
			Version: helmTestVersion,
		},
		Templates: []*chart.File{
			{Name: "templates/_helpers.tpl", Data: []byte(`{{- define "test" -}}parent value{{- end -}}`)},
			{Name: "templates/test.yaml", Data: []byte(`{{ tpl "{{ include \"test\" . }}" . }}`)},
		},
	}
	childChart := &chart.Chart{
		Metadata: &chart.Metadata{
			Name:    helmKeyChartChild,
			Version: helmTestVersion,
		},
		Templates: []*chart.File{
			{Name: "templates/_helpers.tpl", Data: []byte(`{{- define "test" -}}child value{{- end -}}`)},
		},
	}
	parentChart.AddDependency(childChart)

	expect := map[string]string{
		"parent/templates/test.yaml": "parent value",
	}

	for i := range 100 {
		out, err := Render(parentChart, chartutil.Values{})
		if err != nil {
			t.Fatalf("Failed to render templates: %s", err)
		}

		for name, data := range expect {
			if out[name] != data {
				t.Fatalf("Expected %q, got %q (iteration %d)", data, out[name], i+1)
			}
		}
	}
}

func TestRenderInternals(t *testing.T) {
	// Test the internals of the rendering tool.

	vals := chartutil.Values{helmKeyName: "one", "Value": "two"}
	tpls := map[string]renderable{
		"one": {tpl: `Hello {{title .Name}}`, vals: vals},
		"two": {tpl: `Goodbye {{upper .Value}}`, vals: vals},
		// Test whether a template can reliably reference another template
		// without regard for ordering.
		"three": {tpl: `{{template "two" dict "Value" "three"}}`, vals: vals},
	}

	out, err := new(Engine).render(tpls)
	if err != nil {
		t.Fatalf("Failed template rendering: %s", err)
	}

	if len(out) != 3 {
		t.Fatalf("Expected 3 templates, got %d", len(out))
	}

	if out["one"] != "Hello One" {
		t.Errorf("Expected 'Hello One', got %q", out["one"])
	}

	if out["two"] != "Goodbye TWO" {
		t.Errorf("Expected 'Goodbye TWO'. got %q", out["two"])
	}

	if out["three"] != "Goodbye THREE" {
		t.Errorf("Expected 'Goodbye THREE'. got %q", out["two"])
	}
}

func TestRenderWithDNS(t *testing.T) {
	c := &chart.Chart{
		Metadata: &chart.Metadata{
			Name:    "moby",
			Version: helmTestVersion,
		},
		Templates: []*chart.File{
			{Name: "templates/test1", Data: []byte("{{getHostByName \"helm.sh\"}}")},
		},
		Values: map[string]any{},
	}

	vals := map[string]any{
		helmKeyValues: map[string]any{},
	}

	v, err := chartutil.CoalesceValues(c, vals)
	if err != nil {
		t.Fatalf("Failed to coalesce values: %s", err)
	}

	var e Engine
	e.EnableDNS = true
	out, err := e.Render(c, v)
	if err != nil {
		t.Errorf("Failed to render templates: %s", err)
	}

	for _, val := range c.Templates {
		fp := path.Join("moby", val.Name)
		if out[fp] == "" {
			t.Errorf("Expected IP address, got %q", out[fp])
		}
	}
}

func TestParallelRenderInternals(t *testing.T) {
	// Make sure that we can use one Engine to run parallel template renders.
	e := new(Engine)
	var wg sync.WaitGroup
	for i := range 20 {
		wg.Add(1)
		go func(i int) {
			tt := fmt.Sprintf("expect-%d", i)
			tpls := map[string]renderable{
				"t": {
					tpl:  `{{.val}}`,
					vals: map[string]any{"val": tt},
				},
			}
			out, err := e.render(tpls)
			if err != nil {
				t.Errorf("Failed to render %s: %s", tt, err)
			}
			if out["t"] != tt {
				t.Errorf("Expected %q, got %q", tt, out["t"])
			}
			wg.Done()
		}(i)
	}
	wg.Wait()
}

func TestParseErrors(t *testing.T) {
	vals := chartutil.Values{helmKeyValues: map[string]any{}}

	tplsUndefinedFunction := map[string]renderable{
		"undefined_function": {tpl: `{{foo}}`, vals: vals},
	}
	_, err := new(Engine).render(tplsUndefinedFunction)
	if err == nil {
		t.Fatalf("Expected failures while rendering: %s", err)
	}
	expected := `parse error at (undefined_function:1): function "foo" not defined`
	if err.Error() != expected {
		t.Errorf("Expected '%s', got %q", expected, err.Error())
	}
}

func TestExecErrors(t *testing.T) {
	vals := chartutil.Values{helmKeyValues: map[string]any{}}
	cases := []struct {
		name     string
		tpls     map[string]renderable
		expected string
	}{
		{
			name: "MissingRequired",
			tpls: map[string]renderable{
				"missing_required": {tpl: `{{required "foo is required" .Values.foo}}`, vals: vals},
			},
			expected: `execution error at (missing_required:1:2): foo is required`,
		},
		{
			name: "MissingRequiredWithColons",
			tpls: map[string]renderable{
				"missing_required_with_colons": {tpl: `{{required ":this: message: has many: colons:" .Values.foo}}`, vals: vals},
			},
			expected: `execution error at (missing_required_with_colons:1:2): :this: message: has many: colons:`,
		},
		{
			name: "Issue6044",
			tpls: map[string]renderable{
				"issue6044": {
					vals: vals,
					tpl: `{{ $someEmptyValue := "" }}
{{ $myvar := "abc" }}
{{- required (printf "%s: something is missing" $myvar) $someEmptyValue | repeat 0 }}`,
				},
			},
			expected: `execution error at (issue6044:3:4): abc: something is missing`,
		},
		{
			name: "MissingRequiredWithNewlines",
			tpls: map[string]renderable{
				helmFixtureIssue9981: {tpl: `{{required "foo is required\nmore info after the break" .Values.foo}}`, vals: vals},
			},
			expected: `execution error at (issue9981:1:2): foo is required
more info after the break`,
		},
		{
			name: "FailWithNewlines",
			tpls: map[string]renderable{
				helmFixtureIssue9981: {tpl: `{{fail "something is wrong\nlinebreak"}}`, vals: vals},
			},
			expected: `execution error at (issue9981:1:2): something is wrong
linebreak`,
		},
	}

	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			_, err := new(Engine).render(tt.tpls)
			if err == nil {
				t.Fatalf("Expected failures while rendering: %s", err)
			}
			if err.Error() != tt.expected {
				t.Errorf("Expected %q, got %q", tt.expected, err.Error())
			}
		})
	}
}

func TestFailErrors(t *testing.T) {
	vals := chartutil.Values{helmKeyValues: map[string]any{}}

	failtpl := `All your base are belong to us{{ fail "This is an error" }}`
	tplsFailed := map[string]renderable{
		"failtpl": {tpl: failtpl, vals: vals},
	}
	_, err := new(Engine).render(tplsFailed)
	if err == nil {
		t.Fatalf("Expected failures while rendering: %s", err)
	}
	expected := `execution error at (failtpl:1:33): This is an error`
	if err.Error() != expected {
		t.Errorf("Expected '%s', got %q", expected, err.Error())
	}

	var e Engine
	e.LintMode = true
	out, err := e.render(tplsFailed)
	if err != nil {
		t.Fatal(err)
	}

	expectStr := helmFixtureBaseAreUs
	if gotStr := out["failtpl"]; gotStr != expectStr {
		t.Errorf("Expected %q, got %q (%v)", expectStr, gotStr, out)
	}
}

func TestAllTemplates(t *testing.T) {
	ch1 := &chart.Chart{
		Metadata: &chart.Metadata{Name: "ch1"},
		Templates: []*chart.File{
			{Name: "templates/foo", Data: []byte("foo")},
			{Name: "templates/bar", Data: []byte("bar")},
		},
	}
	dep1 := &chart.Chart{
		Metadata: &chart.Metadata{Name: "laboratory mice"},
		Templates: []*chart.File{
			{Name: "templates/pinky", Data: []byte("pinky")},
			{Name: "templates/brain", Data: []byte("brain")},
		},
	}
	ch1.AddDependency(dep1)

	dep2 := &chart.Chart{
		Metadata: &chart.Metadata{Name: "same thing we do every night"},
		Templates: []*chart.File{
			{Name: "templates/innermost", Data: []byte("innermost")},
		},
	}
	dep1.AddDependency(dep2)

	tpls := allTemplates(ch1, chartutil.Values{})
	if len(tpls) != 5 {
		t.Errorf("Expected 5 charts, got %d", len(tpls))
	}
}

func TestChartValuesContainsIsRoot(t *testing.T) {
	ch1 := &chart.Chart{
		Metadata: &chart.Metadata{Name: "parent"},
		Templates: []*chart.File{
			{Name: "templates/isroot", Data: []byte("{{.Chart.IsRoot}}")},
		},
	}
	dep1 := &chart.Chart{
		Metadata: &chart.Metadata{Name: helmKeyChartChild},
		Templates: []*chart.File{
			{Name: "templates/isroot", Data: []byte("{{.Chart.IsRoot}}")},
		},
	}
	ch1.AddDependency(dep1)

	out, err := Render(ch1, chartutil.Values{})
	if err != nil {
		t.Fatalf("failed to render templates: %s", err)
	}
	expects := map[string]string{
		"parent/charts/child/templates/isroot": "false",
		"parent/templates/isroot":              "true",
	}
	for file, expect := range expects {
		if out[file] != expect {
			t.Errorf("Expected %q, got %q", expect, out[file])
		}
	}
}

func TestRenderDependency(t *testing.T) {
	deptpl := `{{define "myblock"}}World{{end}}`
	toptpl := `Hello {{template "myblock"}}`
	ch := &chart.Chart{
		Metadata: &chart.Metadata{Name: "outerchart"},
		Templates: []*chart.File{
			{Name: "templates/outer", Data: []byte(toptpl)},
		},
	}
	ch.AddDependency(&chart.Chart{
		Metadata: &chart.Metadata{Name: "innerchart"},
		Templates: []*chart.File{
			{Name: "templates/inner", Data: []byte(deptpl)},
		},
	})

	out, err := Render(ch, map[string]any{})
	if err != nil {
		t.Fatalf("failed to render chart: %s", err)
	}

	if len(out) != 2 {
		t.Errorf("Expected 2, got %d", len(out))
	}

	expect := "Hello World"
	if out["outerchart/templates/outer"] != expect {
		t.Errorf("Expected %q, got %q", expect, out["outer"])
	}
}

func TestRenderNestedValues(t *testing.T) {
	innerpath := "templates/inner.tpl"
	outerpath := "templates/outer.tpl"
	// Ensure namespacing rules are working.
	deepestpath := "templates/inner.tpl"
	checkrelease := "templates/release.tpl"
	// Ensure subcharts scopes are working.
	subchartspath := "templates/subcharts.tpl"

	deepest := &chart.Chart{
		Metadata: &chart.Metadata{Name: helmFixtureDeepest},
		Templates: []*chart.File{
			{Name: deepestpath, Data: []byte(`And this same {{.Values.what}} that smiles {{.Values.global.when}}`)},
			{Name: checkrelease, Data: []byte(`Tomorrow will be {{default "happy" .Release.Name }}`)},
		},
		Values: map[string]any{"what": "milkshake", "where": "here"},
	}

	inner := &chart.Chart{
		Metadata: &chart.Metadata{Name: helmFixtureHerrick},
		Templates: []*chart.File{
			{Name: innerpath, Data: []byte(`Old {{.Values.who}} is still a-flyin'`)},
		},
		Values: map[string]any{"who": "Robert", "what": "glasses"},
	}
	inner.AddDependency(deepest)

	outer := &chart.Chart{
		Metadata: &chart.Metadata{Name: "top"},
		Templates: []*chart.File{
			{Name: outerpath, Data: []byte(`Gather ye {{.Values.what}} while ye may`)},
			{Name: subchartspath, Data: []byte(`The glorious Lamp of {{.Subcharts.herrick.Subcharts.deepest.Values.where}}, the {{.Subcharts.herrick.Values.what}}`)},
		},
		Values: map[string]any{
			"what": "stinkweed",
			"who":  "me",
			helmFixtureHerrick: map[string]any{
				"who":  "time",
				"what": "Sun",
			},
		},
	}
	outer.AddDependency(inner)

	injValues := map[string]any{
		"what": "rosebuds",
		helmFixtureHerrick: map[string]any{
			helmFixtureDeepest: map[string]any{
				"what":  "flower",
				"where": "Heaven",
			},
		},
		"global": map[string]any{
			"when": "to-day",
		},
	}

	tmp, err := chartutil.CoalesceValues(outer, injValues)
	if err != nil {
		t.Fatalf("Failed to coalesce values: %s", err)
	}

	inject := chartutil.Values{
		helmKeyValues: tmp,
		helmKeyChart:  outer.Metadata,
		helmKeyRelease: chartutil.Values{
			helmKeyName: "dyin",
		},
	}

	t.Logf("Calculated values: %v", inject)

	out, err := Render(outer, inject)
	if err != nil {
		t.Fatalf("failed to render templates: %s", err)
	}

	fullouterpath := "top/" + outerpath
	if out[fullouterpath] != "Gather ye rosebuds while ye may" {
		t.Errorf("Unexpected outer: %q", out[fullouterpath])
	}

	fullinnerpath := "top/charts/herrick/" + innerpath
	if out[fullinnerpath] != "Old time is still a-flyin'" {
		t.Errorf("Unexpected inner: %q", out[fullinnerpath])
	}

	fulldeepestpath := "top/charts/herrick/charts/deepest/" + deepestpath
	if out[fulldeepestpath] != "And this same flower that smiles to-day" {
		t.Errorf("Unexpected deepest: %q", out[fulldeepestpath])
	}

	fullcheckrelease := "top/charts/herrick/charts/deepest/" + checkrelease
	if out[fullcheckrelease] != "Tomorrow will be dyin" {
		t.Errorf("Unexpected release: %q", out[fullcheckrelease])
	}

	fullchecksubcharts := "top/" + subchartspath
	if out[fullchecksubcharts] != "The glorious Lamp of Heaven, the Sun" {
		t.Errorf("Unexpected subcharts: %q", out[fullchecksubcharts])
	}
}

func TestRenderBuiltinValues(t *testing.T) {
	inner := &chart.Chart{
		Metadata: &chart.Metadata{Name: "Latium"},
		Templates: []*chart.File{
			{Name: "templates/Lavinia", Data: []byte(`{{.Template.Name}}{{.Chart.Name}}{{.Release.Name}}`)},
			{Name: "templates/From", Data: []byte(`{{.Files.author | printf "%s"}} {{.Files.Get "book/title.txt"}}`)},
		},
		Files: []*chart.File{
			{Name: "author", Data: []byte("Virgil")},
			{Name: "book/title.txt", Data: []byte("Aeneid")},
		},
	}

	outer := &chart.Chart{
		Metadata: &chart.Metadata{Name: "Troy"},
		Templates: []*chart.File{
			{Name: "templates/Aeneas", Data: []byte(`{{.Template.Name}}{{.Chart.Name}}{{.Release.Name}}`)},
			{Name: "templates/Amata", Data: []byte(`{{.Subcharts.Latium.Chart.Name}} {{.Subcharts.Latium.Files.author | printf "%s"}}`)},
		},
	}
	outer.AddDependency(inner)

	inject := chartutil.Values{
		helmKeyValues: "",
		helmKeyChart:  outer.Metadata,
		helmKeyRelease: chartutil.Values{
			helmKeyName: "Aeneid",
		},
	}

	t.Logf("Calculated values: %v", outer)

	out, err := Render(outer, inject)
	if err != nil {
		t.Fatalf("failed to render templates: %s", err)
	}

	expects := map[string]string{
		"Troy/charts/Latium/templates/Lavinia": "Troy/charts/Latium/templates/LaviniaLatiumAeneid",
		"Troy/templates/Aeneas":                "Troy/templates/AeneasTroyAeneid",
		"Troy/templates/Amata":                 "Latium Virgil",
		"Troy/charts/Latium/templates/From":    "Virgil Aeneid",
	}
	for file, expect := range expects {
		if out[file] != expect {
			t.Errorf("Expected %q, got %q", expect, out[file])
		}
	}
}

func TestAlterFuncMap_include(t *testing.T) {
	c := &chart.Chart{
		Metadata: &chart.Metadata{Name: "conrad"},
		Templates: []*chart.File{
			{Name: "templates/quote", Data: []byte(`{{include "conrad/templates/_partial" . | indent 2}} dead.`)},
			{Name: "templates/_partial", Data: []byte(`{{.Release.Name}} - he`)},
		},
	}

	// Check nested reference in include FuncMap
	d := &chart.Chart{
		Metadata: &chart.Metadata{Name: "nested"},
		Templates: []*chart.File{
			{Name: "templates/quote", Data: []byte(`{{include "nested/templates/quote" . | indent 2}} dead.`)},
			{Name: "templates/_partial", Data: []byte(`{{.Release.Name}} - he`)},
		},
	}

	v := chartutil.Values{
		helmKeyValues: "",
		helmKeyChart:  c.Metadata,
		helmKeyRelease: chartutil.Values{
			helmKeyName: "Mistah Kurtz",
		},
	}

	out, err := Render(c, v)
	if err != nil {
		t.Fatal(err)
	}

	expect := "  Mistah Kurtz - he dead."
	if got := out["conrad/templates/quote"]; got != expect {
		t.Errorf("Expected %q, got %q (%v)", expect, got, out)
	}

	_, err = Render(d, v)
	expectErrName := "nested/templates/quote"
	if err == nil {
		t.Errorf("Expected err of nested reference name: %v", expectErrName)
	}
}

func TestAlterFuncMap_require(t *testing.T) {
	c := &chart.Chart{
		Metadata: &chart.Metadata{Name: "conan"},
		Templates: []*chart.File{
			{Name: "templates/quote", Data: []byte(`All your base are belong to {{ required "A valid 'who' is required" .Values.who }}`)},
			{Name: "templates/bases", Data: []byte(`All {{ required "A valid 'bases' is required" .Values.bases }} of them!`)},
		},
	}

	v := chartutil.Values{
		helmKeyValues: chartutil.Values{
			"who":   "us",
			"bases": 2,
		},
		helmKeyChart: c.Metadata,
		helmKeyRelease: chartutil.Values{
			helmKeyName: helmFixture90sMeme,
		},
	}

	out, err := Render(c, v)
	if err != nil {
		t.Fatal(err)
	}

	expectStr := helmFixtureBaseAreUs
	if gotStr := out["conan/templates/quote"]; gotStr != expectStr {
		t.Errorf("Expected %q, got %q (%v)", expectStr, gotStr, out)
	}
	expectNum := "All 2 of them!"
	if gotNum := out["conan/templates/bases"]; gotNum != expectNum {
		t.Errorf("Expected %q, got %q (%v)", expectNum, gotNum, out)
	}

	// test required without passing in needed values with lint mode on
	// verifies lint replaces required with an empty string (should not fail)
	lintValues := chartutil.Values{
		helmKeyValues: chartutil.Values{
			"who": "us",
		},
		helmKeyChart: c.Metadata,
		helmKeyRelease: chartutil.Values{
			helmKeyName: helmFixture90sMeme,
		},
	}
	var e Engine
	e.LintMode = true
	out, err = e.Render(c, lintValues)
	if err != nil {
		t.Fatal(err)
	}

	expectStr = helmFixtureBaseAreUs
	if gotStr := out["conan/templates/quote"]; gotStr != expectStr {
		t.Errorf("Expected %q, got %q (%v)", expectStr, gotStr, out)
	}
	expectNum = "All  of them!"
	if gotNum := out["conan/templates/bases"]; gotNum != expectNum {
		t.Errorf("Expected %q, got %q (%v)", expectNum, gotNum, out)
	}
}

func TestAlterFuncMap_tpl(t *testing.T) {
	c := &chart.Chart{
		Metadata: &chart.Metadata{Name: helmFixtureTplFunction},
		Templates: []*chart.File{
			{Name: "templates/base", Data: []byte(`Evaluate tpl {{tpl "Value: {{ .Values.value}}" .}}`)},
		},
	}

	v := chartutil.Values{
		helmKeyValues: chartutil.Values{
			"value": "myvalue",
		},
		helmKeyChart: c.Metadata,
		helmKeyRelease: chartutil.Values{
			helmKeyName: helmFixtureTestRelease,
		},
	}

	out, err := Render(c, v)
	if err != nil {
		t.Fatal(err)
	}

	expect := "Evaluate tpl Value: myvalue"
	if got := out["TplFunction/templates/base"]; got != expect {
		t.Errorf("Expected %q, got %q (%v)", expect, got, out)
	}
}

func TestAlterFuncMap_tplfunc(t *testing.T) {
	c := &chart.Chart{
		Metadata: &chart.Metadata{Name: helmFixtureTplFunction},
		Templates: []*chart.File{
			{Name: "templates/base", Data: []byte(`Evaluate tpl {{tpl "Value: {{ .Values.value | quote}}" .}}`)},
		},
	}

	v := chartutil.Values{
		helmKeyValues: chartutil.Values{
			"value": "myvalue",
		},
		helmKeyChart: c.Metadata,
		helmKeyRelease: chartutil.Values{
			helmKeyName: helmFixtureTestRelease,
		},
	}

	out, err := Render(c, v)
	if err != nil {
		t.Fatal(err)
	}

	expect := "Evaluate tpl Value: \"myvalue\""
	if got := out["TplFunction/templates/base"]; got != expect {
		t.Errorf("Expected %q, got %q (%v)", expect, got, out)
	}
}

func TestAlterFuncMap_tplinclude(t *testing.T) {
	c := &chart.Chart{
		Metadata: &chart.Metadata{Name: helmFixtureTplFunction},
		Templates: []*chart.File{
			{Name: "templates/base", Data: []byte(`{{ tpl "{{include ` + "`" + `TplFunction/templates/_partial` + "`" + ` .  | quote }}" .}}`)},
			{Name: "templates/_partial", Data: []byte(`{{.Template.Name}}`)},
		},
	}
	v := chartutil.Values{
		helmKeyValues: chartutil.Values{
			"value": "myvalue",
		},
		helmKeyChart: c.Metadata,
		helmKeyRelease: chartutil.Values{
			helmKeyName: helmFixtureTestRelease,
		},
	}

	out, err := Render(c, v)
	if err != nil {
		t.Fatal(err)
	}

	expect := "\"TplFunction/templates/base\""
	if got := out["TplFunction/templates/base"]; got != expect {
		t.Errorf("Expected %q, got %q (%v)", expect, got, out)
	}
}

func TestRenderRecursionLimit(t *testing.T) {
	// endless recursion should produce an error
	c := &chart.Chart{
		Metadata: &chart.Metadata{Name: "bad"},
		Templates: []*chart.File{
			{Name: "templates/base", Data: []byte(`{{include "recursion" . }}`)},
			{Name: "templates/recursion", Data: []byte(`{{define "recursion"}}{{include "recursion" . }}{{end}}`)},
		},
	}
	v := chartutil.Values{
		helmKeyValues: "",
		helmKeyChart:  c.Metadata,
		helmKeyRelease: chartutil.Values{
			helmKeyName: helmFixtureTestRelease,
		},
	}
	expectErr := "rendering template has a nested reference name: recursion: unable to execute template"

	_, err := Render(c, v)
	if err == nil || !strings.HasSuffix(err.Error(), expectErr) {
		t.Errorf("Expected err with suffix: %s", expectErr)
	}

	// calling the same function many times is ok
	times := 4000
	phrase := "All work and no play makes Jack a dull boy"
	printFunc := `{{define "overlook"}}{{printf "` + phrase + `\n"}}{{end}}`
	repeatedIncl := strings.Repeat(`{{include "overlook" . }}`, times)

	d := &chart.Chart{
		Metadata: &chart.Metadata{Name: "overlook"},
		Templates: []*chart.File{
			{Name: "templates/quote", Data: []byte(repeatedIncl)},
			{Name: "templates/_function", Data: []byte(printFunc)},
		},
	}

	out, err := Render(d, v)
	if err != nil {
		t.Fatal(err)
	}

	expect := strings.Repeat(phrase+"\n", times)
	if got := out["overlook/templates/quote"]; got != expect {
		t.Errorf("Expected %q, got %q (%v)", expect, got, out)
	}
}

func TestRenderLoadTemplateForTplFromFile(t *testing.T) {
	c := &chart.Chart{
		Metadata: &chart.Metadata{Name: "TplLoadFromFile"},
		Templates: []*chart.File{
			{Name: "templates/base", Data: []byte(`{{ tpl (.Files.Get .Values.filename) . }}`)},
			{Name: "templates/_function", Data: []byte(`{{define "test-function"}}test-function{{end}}`)},
		},
		Files: []*chart.File{
			{Name: "test", Data: []byte(`{{ tpl (.Files.Get .Values.filename2) .}}`)},
			{Name: "test2", Data: []byte(`{{include "test-function" .}}{{define "nested-define"}}nested-define-content{{end}} {{include "nested-define" .}}`)},
		},
	}

	v := chartutil.Values{
		helmKeyValues: chartutil.Values{
			"filename":  "test",
			"filename2": "test2",
		},
		helmKeyChart: c.Metadata,
		helmKeyRelease: chartutil.Values{
			helmKeyName: helmFixtureTestRelease,
		},
	}

	out, err := Render(c, v)
	if err != nil {
		t.Fatal(err)
	}

	expect := "test-function nested-define-content"
	if got := out["TplLoadFromFile/templates/base"]; got != expect {
		t.Fatalf("Expected %q, got %q", expect, got)
	}
}

func TestRenderTplEmpty(t *testing.T) {
	c := &chart.Chart{
		Metadata: &chart.Metadata{Name: "TplEmpty"},
		Templates: []*chart.File{
			{Name: "templates/empty-string", Data: []byte(`{{tpl "" .}}`)},
			{Name: "templates/empty-action", Data: []byte(`{{tpl "{{ \"\"}}" .}}`)},
			{Name: "templates/only-defines", Data: []byte(`{{tpl "{{define \"not-invoked\"}}not-rendered{{end}}" .}}`)},
		},
	}
	v := chartutil.Values{
		helmKeyChart: c.Metadata,
		helmKeyRelease: chartutil.Values{
			helmKeyName: helmFixtureTestRelease,
		},
	}

	out, err := Render(c, v)
	if err != nil {
		t.Fatal(err)
	}

	expects := map[string]string{
		"TplEmpty/templates/empty-string": "",
		"TplEmpty/templates/empty-action": "",
		"TplEmpty/templates/only-defines": "",
	}
	for file, expect := range expects {
		if out[file] != expect {
			t.Errorf("Expected %q, got %q", expect, out[file])
		}
	}
}

func TestRenderTplTemplateNames(t *testing.T) {
	// .Template.BasePath and .Name make it through
	c := &chart.Chart{
		Metadata: &chart.Metadata{Name: "TplTemplateNames"},
		Templates: []*chart.File{
			{Name: "templates/default-basepath", Data: []byte(`{{tpl "{{ .Template.BasePath }}" .}}`)},
			{Name: "templates/default-name", Data: []byte(`{{tpl "{{ .Template.Name }}" .}}`)},
			{Name: "templates/modified-basepath", Data: []byte(`{{tpl "{{ .Template.BasePath }}" .Values.dot}}`)},
			{Name: "templates/modified-name", Data: []byte(`{{tpl "{{ .Template.Name }}" .Values.dot}}`)},
			{Name: "templates/modified-field", Data: []byte(`{{tpl "{{ .Template.Field }}" .Values.dot}}`)},
		},
	}
	v := chartutil.Values{
		helmKeyValues: chartutil.Values{
			"dot": chartutil.Values{
				"Template": chartutil.Values{
					helmKeyBasePath: "path/to/template",
					helmKeyName:     "name-of-template",
					"Field":         helmFixtureExtraField,
				},
			},
		},
		helmKeyChart: c.Metadata,
		helmKeyRelease: chartutil.Values{
			helmKeyName: helmFixtureTestRelease,
		},
	}

	out, err := Render(c, v)
	if err != nil {
		t.Fatal(err)
	}

	expects := map[string]string{
		"TplTemplateNames/templates/default-basepath":  "TplTemplateNames/templates",
		"TplTemplateNames/templates/default-name":      "TplTemplateNames/templates/default-name",
		"TplTemplateNames/templates/modified-basepath": "path/to/template",
		"TplTemplateNames/templates/modified-name":     "name-of-template",
		"TplTemplateNames/templates/modified-field":    helmFixtureExtraField,
	}
	for file, expect := range expects {
		if out[file] != expect {
			t.Errorf("Expected %q, got %q", expect, out[file])
		}
	}
}

func TestRenderTplRedefines(t *testing.T) {
	// Redefining a template inside 'tpl' does not affect the outer definition
	c := &chart.Chart{
		Metadata: &chart.Metadata{Name: "TplRedefines"},
		Templates: []*chart.File{
			{Name: "templates/_partials", Data: []byte(`{{define "partial"}}original-in-partial{{end}}`)},
			{Name: "templates/partial", Data: []byte(
				`before: {{include "partial" .}}\n{{tpl .Values.partialText .}}\nafter: {{include "partial" .}}`,
			)},
			{Name: "templates/manifest", Data: []byte(
				`{{define "manifest"}}original-in-manifest{{end}}` +
					`before: {{include "manifest" .}}\n{{tpl .Values.manifestText .}}\nafter: {{include "manifest" .}}`,
			)},
			{Name: "templates/manifest-only", Data: []byte(
				`{{define "manifest-only"}}only-in-manifest{{end}}` +
					`before: {{include "manifest-only" .}}\n{{tpl .Values.manifestOnlyText .}}\nafter: {{include "manifest-only" .}}`,
			)},
			{Name: "templates/nested", Data: []byte(
				`{{define "nested"}}original-in-manifest{{end}}` +
					`{{define "nested-outer"}}original-outer-in-manifest{{end}}` +
					`before: {{include "nested" .}} {{include "nested-outer" .}}\n` +
					`{{tpl .Values.nestedText .}}\n` +
					`after: {{include "nested" .}} {{include "nested-outer" .}}`,
			)},
		},
	}
	v := chartutil.Values{
		helmKeyValues: chartutil.Values{
			"partialText":      `{{define "partial"}}redefined-in-tpl{{end}}tpl: {{include "partial" .}}`,
			"manifestText":     `{{define "manifest"}}redefined-in-tpl{{end}}tpl: {{include "manifest" .}}`,
			"manifestOnlyText": `tpl: {{include "manifest-only" .}}`,
			"nestedText": `{{define "nested"}}redefined-in-tpl{{end}}` +
				`{{define "nested-outer"}}redefined-outer-in-tpl{{end}}` +
				`before-inner-tpl: {{include "nested" .}} {{include "nested-outer" . }}\n` +
				`{{tpl .Values.innerText .}}\n` +
				`after-inner-tpl: {{include "nested" .}} {{include "nested-outer" . }}`,
			"innerText": `{{define "nested"}}redefined-in-inner-tpl{{end}}inner-tpl: {{include "nested" .}} {{include "nested-outer" . }}`,
		},
		helmKeyChart: c.Metadata,
		helmKeyRelease: chartutil.Values{
			helmKeyName: helmFixtureTestRelease,
		},
	}

	out, err := Render(c, v)
	if err != nil {
		t.Fatal(err)
	}

	expects := map[string]string{
		"TplRedefines/templates/partial":       `before: original-in-partial\ntpl: redefined-in-tpl\nafter: original-in-partial`,
		"TplRedefines/templates/manifest":      `before: original-in-manifest\ntpl: redefined-in-tpl\nafter: original-in-manifest`,
		"TplRedefines/templates/manifest-only": `before: only-in-manifest\ntpl: only-in-manifest\nafter: only-in-manifest`,
		"TplRedefines/templates/nested": `before: original-in-manifest original-outer-in-manifest\n` +
			`before-inner-tpl: redefined-in-tpl redefined-outer-in-tpl\n` +
			`inner-tpl: redefined-in-inner-tpl redefined-outer-in-tpl\n` +
			`after-inner-tpl: redefined-in-tpl redefined-outer-in-tpl\n` +
			`after: original-in-manifest original-outer-in-manifest`,
	}
	for file, expect := range expects {
		if out[file] != expect {
			t.Errorf("Expected %q, got %q", expect, out[file])
		}
	}
}

func TestRenderTplMissingKey(t *testing.T) {
	// Rendering a missing key results in empty/zero output.
	c := &chart.Chart{
		Metadata: &chart.Metadata{Name: "TplMissingKey"},
		Templates: []*chart.File{
			{Name: "templates/manifest", Data: []byte(
				`missingValue: {{tpl "{{.Values.noSuchKey}}" .}}`,
			)},
		},
	}
	v := chartutil.Values{
		helmKeyValues: chartutil.Values{},
		helmKeyChart:  c.Metadata,
		helmKeyRelease: chartutil.Values{
			helmKeyName: helmFixtureTestRelease,
		},
	}

	out, err := Render(c, v)
	if err != nil {
		t.Fatal(err)
	}

	expects := map[string]string{
		"TplMissingKey/templates/manifest": `missingValue: `,
	}
	for file, expect := range expects {
		if out[file] != expect {
			t.Errorf("Expected %q, got %q", expect, out[file])
		}
	}
}

func TestRenderTplMissingKeyString(t *testing.T) {
	// Rendering a missing key results in error
	c := &chart.Chart{
		Metadata: &chart.Metadata{Name: "TplMissingKeyStrict"},
		Templates: []*chart.File{
			{Name: "templates/manifest", Data: []byte(
				`missingValue: {{tpl "{{.Values.noSuchKey}}" .}}`,
			)},
		},
	}
	v := chartutil.Values{
		helmKeyValues: chartutil.Values{},
		helmKeyChart:  c.Metadata,
		helmKeyRelease: chartutil.Values{
			helmKeyName: helmFixtureTestRelease,
		},
	}

	e := new(Engine)
	e.Strict = true

	out, err := e.Render(c, v)
	if err == nil {
		t.Errorf("Expected error, got %v", out)
		return
	}
	var execErr template.ExecError
	if !errors.As(err, &execErr) {
		// Some unexpected error.
		t.Fatal(err)
	}
	errTxt := fmt.Sprint(err)
	if !strings.Contains(errTxt, "noSuchKey") {
		t.Errorf("Expected error to contain 'noSuchKey', got %s", errTxt)
	}
}

func TestTalosVersionInTemplateContext(t *testing.T) {
	t.Parallel()

	c := &chart.Chart{
		Metadata: &chart.Metadata{
			Name:    "testchart",
			Version: "0.1.0",
		},
		Templates: []*chart.File{
			{Name: "templates/test.yaml", Data: []byte("talosVersion: {{ .TalosVersion }}")},
		},
	}

	vals := chartutil.Values{
		helmKeyValues:  chartutil.Values{},
		"TalosVersion": "v1.12",
	}

	out, err := Render(c, vals)
	if err != nil {
		t.Fatalf("failed to render: %v", err)
	}

	result := out["testchart/templates/test.yaml"]
	expected := "talosVersion: v1.12"
	if strings.TrimSpace(result) != expected {
		t.Errorf("expected %q, got %q", expected, strings.TrimSpace(result))
	}
}

func TestTalosVersionEmpty(t *testing.T) {
	t.Parallel()

	c := &chart.Chart{
		Metadata: &chart.Metadata{
			Name:    "testchart",
			Version: "0.1.0",
		},
		Templates: []*chart.File{
			{Name: "templates/test.yaml", Data: []byte("talosVersion: {{ .TalosVersion }}")},
		},
	}

	vals := chartutil.Values{
		helmKeyValues: chartutil.Values{},
	}

	out, err := Render(c, vals)
	if err != nil {
		t.Fatalf("failed to render: %v", err)
	}

	result := out["testchart/templates/test.yaml"]
	expected := "talosVersion:"
	if strings.TrimSpace(result) != expected {
		t.Errorf("expected %q, got %q", expected, strings.TrimSpace(result))
	}
}

func TestTalosVersionConcurrentRender(t *testing.T) {
	t.Parallel()

	renderWithVersion := func(version string, expected string) {
		c := &chart.Chart{
			Metadata: &chart.Metadata{
				Name:    "testchart",
				Version: "0.1.0",
			},
			Templates: []*chart.File{
				{Name: "templates/test.yaml", Data: []byte("talosVersion: {{ .TalosVersion }}")},
			},
		}
		vals := chartutil.Values{
			helmKeyValues:  chartutil.Values{},
			"TalosVersion": version,
		}
		out, err := Render(c, vals)
		if err != nil {
			t.Errorf("render with version %q failed: %v", version, err)
			return
		}
		result := strings.TrimSpace(out["testchart/templates/test.yaml"])
		if result != expected {
			t.Errorf("version %q: expected %q, got %q", version, expected, result)
		}
	}

	var wg sync.WaitGroup
	for range 10 {
		wg.Add(2)
		go func() {
			defer wg.Done()
			renderWithVersion("v1.12", "talosVersion: v1.12")
		}()
		go func() {
			defer wg.Done()
			renderWithVersion("v1.11", "talosVersion: v1.11")
		}()
	}
	wg.Wait()
}

// TestCidrNetworkTemplateFunc exercises the cidrNetwork template
// function directly (bypassing chart rendering) so a future refactor
// that breaks parsing or masking — for either IPv4 or IPv6 inputs —
// is caught without needing to boot the whole helm engine.
func TestCidrNetworkTemplateFunc(t *testing.T) {
	renderExpr := func(expr string) (string, error) {
		chrt := &chart.Chart{
			Metadata:  &chart.Metadata{Name: "cidrtest"},
			Templates: []*chart.File{{Name: "templates/out.yaml", Data: []byte(expr)}},
			Values:    map[string]any{},
		}
		var eng Engine
		out, err := eng.Render(chrt, chartutil.Values{helmKeyValues: map[string]any{}})
		if err != nil {
			return "", err
		}
		return out["cidrtest/templates/out.yaml"], nil
	}

	tests := []struct {
		name    string
		input   string
		want    string
		wantErr bool
	}{
		{"ipv4 host form", "192.168.201.10/24", "192.168.201.0/24", false},
		{"ipv4 already canonical", "10.0.0.0/8", "10.0.0.0/8", false},
		{"ipv4 narrow prefix", "192.168.201.10/31", "192.168.201.10/31", false},
		{"ipv6 host form", "2001:db8::1/64", "2001:db8::/64", false},
		{"ipv6 already canonical", "fd00::/8", "fd00::/8", false},
		{"malformed missing prefix", "192.168.201.10", "", true},
		{"malformed garbage", "not-a-cidr", "", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := renderExpr(fmt.Sprintf(`{{ cidrNetwork %q }}`, tt.input))
			if tt.wantErr {
				if err == nil {
					t.Errorf("expected error for input %q, got output %q", tt.input, got)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error for %q: %v", tt.input, err)
			}
			if got != tt.want {
				t.Errorf("cidrNetwork(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

// TestCidrContainsTemplateFunc exercises the cidrContains template
// function directly. Used by talm.discovered.link_name_for_address
// to pick the link whose subnet hosts a floatingIP. Both IPv4 and
// IPv6 paths are exercised so a future swap of net/netip for
// per-family bit math surfaces here, not through chart symptoms.
func TestCidrContainsTemplateFunc(t *testing.T) {
	renderExpr := func(expr string) (string, error) {
		chrt := &chart.Chart{
			Metadata:  &chart.Metadata{Name: "cidrtest"},
			Templates: []*chart.File{{Name: "templates/out.yaml", Data: []byte(expr)}},
			Values:    map[string]any{},
		}
		var eng Engine
		out, err := eng.Render(chrt, chartutil.Values{helmKeyValues: map[string]any{}})
		if err != nil {
			return "", err
		}
		return out["cidrtest/templates/out.yaml"], nil
	}

	tests := []struct {
		name    string
		cidr    string
		ip      string
		want    string
		wantErr bool
	}{
		{"ipv4 host inside /24", "192.168.100.0/24", "192.168.100.10", "true", false},
		{"ipv4 host outside /24", "192.168.100.0/24", "192.168.101.10", "false", false},
		{"ipv4 host on boundary /24", "192.168.100.0/24", "192.168.100.0", "true", false},
		{"ipv4 broadcast in /24", "192.168.100.0/24", "192.168.100.255", "true", false},
		{"ipv4 inside /26 first quarter", "88.99.210.0/26", "88.99.210.37", "true", false},
		{"ipv4 outside /26 first quarter", "88.99.210.0/26", "88.99.210.64", "false", false},
		{"ipv4 /32 self-match", "10.0.0.1/32", "10.0.0.1", "true", false},
		{"ipv4 /32 other-host", "10.0.0.1/32", "10.0.0.2", "false", false},
		{"ipv6 inside /64", "2001:db8::/64", "2001:db8::1", "true", false},
		{"ipv6 outside /64", "2001:db8::/64", "2001:db9::1", "false", false},
		{"hetzner case: VIP in private VLAN /24", "192.168.100.4/24", "192.168.100.10", "true", false},
		{"hetzner case: VIP NOT in public /26", "88.99.210.37/26", "192.168.100.10", "false", false},
		// Parse failures fall through to "no match" rather than an
		// error so the chart-side helper that iterates over every
		// address in the COSI table doesn't crash the entire render
		// on a single corrupt or future-format entry. An operator-
		// typoed floatingIP likewise produces a "no match" outcome,
		// which routes through the default-link fallback — Talos
		// rejects the malformed IP literal on apply with a clearer
		// error than the chart could produce.
		{"malformed cidr returns false", "not-a-cidr", "10.0.0.1", "false", false},
		{"malformed ip returns false", "10.0.0.0/24", "not-an-ip", "false", false},
		{"empty cidr returns false", "", "10.0.0.1", "false", false},
		{"empty ip returns false", "10.0.0.0/24", "", "false", false},
		{"both empty returns false", "", "", "false", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := renderExpr(fmt.Sprintf(`{{ cidrContains %q %q }}`, tt.cidr, tt.ip))
			if tt.wantErr {
				if err == nil {
					t.Errorf("expected error for cidr=%q ip=%q, got output %q", tt.cidr, tt.ip, got)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error for cidr=%q ip=%q: %v", tt.cidr, tt.ip, err)
			}
			if got != tt.want {
				t.Errorf("cidrContains(%q, %q) = %q, want %q", tt.cidr, tt.ip, got, tt.want)
			}
		})
	}
}
