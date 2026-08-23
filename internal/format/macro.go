package format

import (
	"encoding/xml"
	"strings"
)

// Macro is one ac:structured-macro found in a storage body.
type Macro struct {
	// Name is the ac:name attribute, e.g. "plantumlcloud".
	Name string
	// LocalID is the ac:local-id attribute, empty when the element carries
	// none. It is the stable identifier across editor saves — Confluence
	// reissues ac:macro-id but keeps this — so it is what a caller selects a
	// macro by.
	LocalID string
	// Params are the macro's direct ac:parameter children, in document order.
	Params []MacroParam
}

// Param returns the parameter with the given name.
func (m Macro) Param(name string) (MacroParam, bool) {
	for _, p := range m.Params {
		if p.Name == name {
			return p, true
		}
	}
	return MacroParam{}, false
}

// MacroParam is one ac:parameter, with both what it says and where it says it.
//
// Value is the text the parameter carries, entity references resolved and
// surrounding whitespace trimmed — the same thing the Markdown converter
// reads. Start and End bound that text in the storage string Macros was given,
// as raw bytes: no entity is resolved there, and no whitespace is trimmed off.
// The two therefore differ for any value written with an entity or padded with
// whitespace, which is why replacing a range is only safe for a value whose
// text and bytes are the same thing — a base64 payload or a number.
type MacroParam struct {
	Name       string
	Value      string
	Start, End int
	// Empty marks a parameter written as a self-closing tag. Such an element
	// has no content, so Start and End meet just past its "/>", outside the
	// parameter — a caller must not write there.
	Empty bool
}

// Macros scans a storage body for its macros, in document order, and reports
// where each parameter's text sits so a caller can rewrite one in place.
//
// It only reads: the returned offsets index the string that was passed in, and
// splicing a new value into one is the caller's business. That is what keeps
// this package out of the edit round-trip while still being the one place that
// knows how to parse a storage body — the scan has to configure its decoder
// exactly as the Markdown conversion does, and a second copy of that
// configuration elsewhere would drift.
func Macros(storage string) []Macro {
	decoder := newStorageDecoder(storage)

	var (
		macros []Macro
		// stack mirrors the open elements, so a parameter is attributed to the
		// macro it is a direct child of rather than to an enclosing one.
		stack []openElement
	)
	for {
		// Captured before the token is read: after an end element this is the
		// offset of its "<", which is where the content before it stops.
		before := int(decoder.InputOffset())

		token, err := decoder.Token()
		if err != nil {
			// EOF, or a document too broken to recover from. Either way,
			// report the macros found so far, the way parseStorage renders
			// what it managed to parse.
			break
		}

		switch t := token.(type) {
		case xml.CharData:
			// An ac:parameter holds flat text, so the open one is always the
			// innermost element.
			if len(stack) > 0 && stack[len(stack)-1].param != nil {
				stack[len(stack)-1].param.Value += string(t)
			}
		case xml.StartElement:
			stack = append(stack, openMacroElement(t, storage, decoder, stack, &macros))
		case xml.EndElement:
			if len(stack) == 0 {
				// A close with nothing open: non-strict parsing recovering
				// from stray markup. Nothing to attribute it to.
				continue
			}
			el := stack[len(stack)-1]
			stack = stack[:len(stack)-1]
			if el.param != nil {
				closeParameter(el.param, before)
			}
		}
	}
	return macros
}

// openElement is one entry of the scan's element stack. macro marks an
// ac:structured-macro, which is what a parameter has to be a direct child of;
// param points at the parameter this element opened, if it opened one.
type openElement struct {
	macro bool
	param *MacroParam
}

// openMacroElement records a start element, appending a macro or a parameter
// as the element calls for, and returns the stack entry to push for it.
//
// The returned entry points straight at the parameter it appended rather than
// carrying indexes to look it up by later. The pointer survives the appends
// that follow because only one parameter is ever open at a time — a
// parameter is closed before the next one is appended — and because growing
// the macro slice copies Macro headers, leaving the Params array a header
// points into where it was.
func openMacroElement(
	t xml.StartElement, storage string, decoder *xml.Decoder, stack []openElement, macros *[]Macro,
) openElement {
	space, local := strings.ToLower(t.Name.Space), strings.ToLower(t.Name.Local)
	if space != "ac" {
		return openElement{}
	}

	switch local {
	case "structured-macro":
		*macros = append(*macros, Macro{Name: attrValue(t, "name"), LocalID: attrValue(t, "local-id")})
		return openElement{macro: true}
	case "parameter":
		// Only a direct child counts, matching what the Markdown converter
		// reads: an ac:parameter deeper inside a macro's body belongs to
		// whatever holds it, not to the macro.
		if len(stack) == 0 || !stack[len(stack)-1].macro {
			return openElement{}
		}
		// After the start element, InputOffset is the byte just past its ">".
		start := int(decoder.InputOffset())
		macro := &(*macros)[len(*macros)-1]
		macro.Params = append(macro.Params, MacroParam{
			Name:  attrValue(t, "name"),
			Start: start,
			End:   start,
			// A self-closing tag ends "/>" and holds nothing, so the range
			// above is not inside the element. XML spells an empty element
			// tag exactly one way, which is what makes this readable off the
			// two bytes behind the offset.
			Empty: start >= 2 && storage[start-2:start] == "/>",
		})
		return openElement{param: &macro.Params[len(macro.Params)-1]}
	}
	return openElement{}
}

// closeParameter finishes the parameter an end element closed.
func closeParameter(p *MacroParam, end int) {
	// A synthesized close — non-strict parsing recovering from an unclosed
	// tag — can land before the content started. There is no range to splice
	// then, so the parameter is marked as having none rather than carrying an
	// inverted one.
	if end < p.Start {
		p.Empty = true
		p.Value = ""
		return
	}
	p.End = end
	p.Value = strings.TrimSpace(p.Value)
}

// attrValue reads an attribute by local name, the way parseStorage does:
// storage prefixes attributes (ac:, ri:) but never reuses one local name for
// two meanings on the same element.
func attrValue(t xml.StartElement, local string) string {
	for _, a := range t.Attr {
		if a.Name.Local == local {
			return a.Value
		}
	}
	return ""
}
