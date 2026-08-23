package format

import (
	"os"
	"slices"
	"testing"
)

// macroNames is what a scan found, for the assertions that only care about
// which macros were reported and in what order.
func macroNames(macros []Macro) []string {
	names := make([]string, 0, len(macros))
	for _, m := range macros {
		names = append(names, m.Name)
	}
	return names
}

func TestMacrosReportsTheRangeOfEveryParameter(t *testing.T) {
	raw, err := os.ReadFile("testdata/plantumlcloud_macro.xml")
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	storage := string(raw)

	macros := Macros(storage)
	if len(macros) != 1 {
		t.Fatalf("Macros() = %d macros, want 1", len(macros))
	}
	m := macros[0]
	if m.Name != "plantumlcloud" {
		t.Errorf("Name = %q, want %q", m.Name, "plantumlcloud")
	}
	if m.LocalID != "7c1d4e8a" {
		t.Errorf("LocalID = %q, want %q", m.LocalID, "7c1d4e8a")
	}

	for _, name := range []string{"toolbar", "filename", "data", "compressed", "revision"} {
		p, ok := m.Param(name)
		if !ok {
			t.Errorf("Param(%q) not found", name)
			continue
		}
		if p.Empty {
			t.Errorf("Param(%q).Empty = true, want false", name)
		}
		// The range is what `set` splices into, so it has to bound exactly the
		// text the parameter carries.
		if got := storage[p.Start:p.End]; got != p.Value {
			t.Errorf("storage[%d:%d] = %q, want %q", p.Start, p.End, got, p.Value)
		}
	}

	if p, _ := m.Param("revision"); p.Value != "1" {
		t.Errorf("revision = %q, want %q", p.Value, "1")
	}
	if p, _ := m.Param("filename"); p.Value != "test.svg" {
		t.Errorf("filename = %q, want %q", p.Value, "test.svg")
	}
	if _, ok := m.Param("nothing"); ok {
		t.Error("Param(\"nothing\") found a parameter that is not there")
	}
}

func TestMacrosReportsEveryMacroInDocumentOrder(t *testing.T) {
	const storage = `<p>text</p>` +
		`<ac:structured-macro ac:name="first" ac:local-id="a"/>` +
		`<ac:structured-macro ac:name="outer"><ac:parameter ac:name="p">outer value</ac:parameter>` +
		`<ac:rich-text-body><ac:structured-macro ac:name="inner">` +
		`<ac:parameter ac:name="p">inner value</ac:parameter></ac:structured-macro></ac:rich-text-body>` +
		`</ac:structured-macro>`

	macros := Macros(storage)
	want := []string{"first", "outer", "inner"}
	if got := macroNames(macros); !slices.Equal(got, want) {
		t.Fatalf("Macros() = %v, want %v", got, want)
	}

	// A parameter belongs to the macro it is a direct child of, so the outer
	// macro must not pick up the nested one's.
	if p, ok := macros[1].Param("p"); !ok || p.Value != "outer value" {
		t.Errorf("outer Param(\"p\") = %q (found %v), want %q", p.Value, ok, "outer value")
	}
	if p, ok := macros[2].Param("p"); !ok || p.Value != "inner value" {
		t.Errorf("inner Param(\"p\") = %q (found %v), want %q", p.Value, ok, "inner value")
	}
	if len(macros[0].Params) != 0 {
		t.Errorf("first macro has %d parameters, want 0", len(macros[0].Params))
	}
}

func TestMacrosLeavesLocalIDEmptyWhenTheElementCarriesNone(t *testing.T) {
	const storage = `<ac:structured-macro ac:name="plantumlcloud" ac:macro-id="m"/>`

	macros := Macros(storage)
	if len(macros) != 1 {
		t.Fatalf("Macros() = %d macros, want 1", len(macros))
	}
	if macros[0].LocalID != "" {
		t.Errorf("LocalID = %q, want it empty", macros[0].LocalID)
	}
}

// TestMacrosMarksAParameterWithNoRangeToSplice covers the shape whose byte
// range is not inside the element at all: a self-closing tag has no content,
// so the offsets meet just past its "/>" and writing there would land outside
// the parameter.
func TestMacrosMarksAParameterWithNoRangeToSplice(t *testing.T) {
	const storage = `<ac:structured-macro ac:name="m">` +
		`<ac:parameter ac:name="selfclosed"/>` +
		`<ac:parameter ac:name="written"></ac:parameter>` +
		`</ac:structured-macro>`

	m := Macros(storage)[0]

	selfClosed, ok := m.Param("selfclosed")
	if !ok {
		t.Fatal("Param(\"selfclosed\") not found")
	}
	if !selfClosed.Empty {
		t.Error("self-closing parameter: Empty = false, want true")
	}
	if selfClosed.Start != selfClosed.End {
		t.Errorf("self-closing parameter: range = [%d:%d], want it empty", selfClosed.Start, selfClosed.End)
	}

	// An empty element written out in full does have a range to splice into,
	// even though it holds no text.
	written, ok := m.Param("written")
	if !ok {
		t.Fatal("Param(\"written\") not found")
	}
	if written.Empty {
		t.Error("written-out empty parameter: Empty = true, want false")
	}
	if got := storage[written.Start:written.End]; got != "" {
		t.Errorf("written-out empty parameter: storage[%d:%d] = %q, want it empty",
			written.Start, written.End, got)
	}
}

// TestMacrosDecodesAParameterValueButNotItsRange pins the one place the two
// halves of a parameter disagree: Value is the text an entity stands for,
// while the range is the bytes as the body wrote them.
func TestMacrosDecodesAParameterValueButNotItsRange(t *testing.T) {
	const storage = `<ac:structured-macro ac:name="m">` +
		`<ac:parameter ac:name="filename">a &amp; b.svg</ac:parameter>` +
		`</ac:structured-macro>`

	p, ok := Macros(storage)[0].Param("filename")
	if !ok {
		t.Fatal("Param(\"filename\") not found")
	}
	if p.Value != "a & b.svg" {
		t.Errorf("Value = %q, want %q", p.Value, "a & b.svg")
	}
	if got := storage[p.Start:p.End]; got != "a &amp; b.svg" {
		t.Errorf("storage[%d:%d] = %q, want %q", p.Start, p.End, got, "a &amp; b.svg")
	}
}

func TestMacrosTrimsTheWhitespaceAroundAValueButNotTheRange(t *testing.T) {
	const storage = "<ac:structured-macro ac:name=\"m\">" +
		"<ac:parameter ac:name=\"revision\">\n    2\n  </ac:parameter>" +
		"</ac:structured-macro>"

	p, ok := Macros(storage)[0].Param("revision")
	if !ok {
		t.Fatal("Param(\"revision\") not found")
	}
	if p.Value != "2" {
		t.Errorf("Value = %q, want %q", p.Value, "2")
	}
	if got := storage[p.Start:p.End]; got != "\n    2\n  " {
		t.Errorf("storage[%d:%d] = %q, want the untrimmed text", p.Start, p.End, got)
	}
}

func TestMacrosReportsNothingForABodyWithNoMacro(t *testing.T) {
	if got := Macros("<p>just text</p>"); len(got) != 0 {
		t.Errorf("Macros() = %v, want none", macroNames(got))
	}
}

// TestMacrosAttributesAParameterFollowingANestedMacro pins the ordering the
// stack exists for: after a nested macro has been read and closed, the macro
// most recently appended is no longer the one the next parameter belongs to.
// Confluence's own serializer writes parameters before a body, so this shape
// comes from bodies written through the API by other tools -- where getting
// it wrong would have `set` rewrite bytes inside an unrelated macro.
func TestMacrosAttributesAParameterFollowingANestedMacro(t *testing.T) {
	const storage = `<ac:structured-macro ac:name="outer">` +
		`<ac:rich-text-body><ac:structured-macro ac:name="inner">` +
		`<ac:parameter ac:name="p">inner value</ac:parameter>` +
		`</ac:structured-macro></ac:rich-text-body>` +
		`<ac:parameter ac:name="p">outer value</ac:parameter>` +
		`</ac:structured-macro>`

	macros := Macros(storage)
	want := []string{"outer", "inner"}
	if got := macroNames(macros); !slices.Equal(got, want) {
		t.Fatalf("Macros() = %v, want %v", got, want)
	}

	outer, inner := macros[0], macros[1]
	if p, ok := outer.Param("p"); !ok || p.Value != "outer value" {
		t.Errorf("outer Param(\"p\") = %q (found %v), want %q", p.Value, ok, "outer value")
	}
	if len(inner.Params) != 1 {
		t.Errorf("inner macro has %d parameters, want 1", len(inner.Params))
	}
	if p, ok := inner.Param("p"); !ok || p.Value != "inner value" {
		t.Errorf("inner Param(\"p\") = %q (found %v), want %q", p.Value, ok, "inner value")
	}
	// The ranges have to follow the attribution, or a splice would land in the
	// other macro.
	if p, _ := outer.Param("p"); storage[p.Start:p.End] != "outer value" {
		t.Errorf("outer range = %q, want %q", storage[p.Start:p.End], "outer value")
	}
}
