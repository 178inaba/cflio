package format

import (
	"slices"
	"testing"
)

// endsAfter renders what ElementEnds reported as the text each offset follows,
// so a case can say which end tag it expected rather than counting bytes.
func endsAfter(storage string, ends []int) []string {
	after := make([]string, 0, len(ends))
	for _, end := range ends {
		after = append(after, storage[:end])
	}
	return after
}

func TestElementEndsFindsBothSpellingsOfTheAttribute(t *testing.T) {
	// Confluence stamps local-id on block elements and ac:local-id on macros.
	// Either is a legitimate anchor, so both have to be found.
	const storage = `<p local-id="block">text</p>` +
		`<ac:structured-macro ac:name="m" ac:local-id="macro">` +
		`<ac:parameter ac:name="p">v</ac:parameter></ac:structured-macro>`

	for _, tc := range []struct {
		id   string
		want string
	}{
		{id: "block", want: `<p local-id="block">text</p>`},
		{id: "macro", want: storage},
	} {
		ends := ElementEnds(storage, tc.id)
		if len(ends) != 1 {
			t.Fatalf("ElementEnds(%q) = %d offsets, want 1", tc.id, len(ends))
		}
		if got := storage[:ends[0]]; got != tc.want {
			t.Errorf("ElementEnds(%q) points past %q, want past %q", tc.id, got, tc.want)
		}
	}
}

func TestElementEndsPointsPastTheEndTagOfANestedElement(t *testing.T) {
	// The inner element ends inside the outer one, and the outer one past its
	// own end tag: an anchor names one element, not the run it sits in.
	const storage = `<ac:layout local-id="outer"><p local-id="inner">text</p></ac:layout><p>after</p>`

	inner := ElementEnds(storage, "inner")
	if got, want := endsAfter(storage, inner),
		[]string{`<ac:layout local-id="outer"><p local-id="inner">text</p>`}; !slices.Equal(got, want) {
		t.Errorf("ElementEnds(%q) points past %q, want past %q", "inner", got, want)
	}

	outer := ElementEnds(storage, "outer")
	if got, want := endsAfter(storage, outer),
		[]string{`<ac:layout local-id="outer"><p local-id="inner">text</p></ac:layout>`}; !slices.Equal(got, want) {
		t.Errorf("ElementEnds(%q) points past %q, want past %q", "outer", got, want)
	}
}

func TestElementEndsPointsPastASelfClosingElement(t *testing.T) {
	// A self-closing element has no end tag of its own; the offset has to land
	// past its "/>" all the same, or an insertion would land inside it.
	const storage = `<p>before</p><ac:structured-macro ac:name="m" ac:local-id="only"/><p>after</p>`

	ends := ElementEnds(storage, "only")
	if got, want := endsAfter(storage, ends),
		[]string{`<p>before</p><ac:structured-macro ac:name="m" ac:local-id="only"/>`}; !slices.Equal(got, want) {
		t.Errorf("ElementEnds(%q) points past %q, want past %q", "only", got, want)
	}
}

func TestElementEndsReportsEveryMatchInDocumentOrder(t *testing.T) {
	// A body should not carry the same id twice, but a caller that acts on the
	// first of two would insert somewhere the caller never named. Reporting
	// both is what lets it refuse instead.
	const storage = `<p local-id="dup">one</p><p local-id="dup">two</p>`

	ends := ElementEnds(storage, "dup")
	if got, want := endsAfter(storage, ends), []string{
		`<p local-id="dup">one</p>`,
		`<p local-id="dup">one</p><p local-id="dup">two</p>`,
	}; !slices.Equal(got, want) {
		t.Errorf("ElementEnds(%q) points past %q, want past %q", "dup", got, want)
	}
}

func TestElementEndsReportsNothingForAnIDNoElementCarries(t *testing.T) {
	const storage = `<p local-id="a">text</p>`

	if ends := ElementEnds(storage, "b"); len(ends) != 0 {
		t.Errorf("ElementEnds(%q) = %v, want no offsets", "b", ends)
	}
	// An absent attribute reads as the empty string, so an empty id would
	// otherwise match every element in the body.
	if ends := ElementEnds(storage, ""); len(ends) != 0 {
		t.Errorf("ElementEnds(\"\") = %v, want no offsets", ends)
	}
}
