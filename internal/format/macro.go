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
	sc := macroScan{storage: storage, decoder: newStorageDecoder(storage)}
	for {
		// Captured before the token is read: after an end element this is the
		// offset of its "<", which is where the content before it stops.
		before := int(sc.decoder.InputOffset())

		token, err := sc.decoder.Token()
		if err != nil {
			// EOF, or a document too broken to recover from. Either way,
			// report the macros found so far, the way parseStorage renders
			// what it managed to parse.
			break
		}

		switch t := token.(type) {
		case xml.CharData:
			sc.charData(t)
		case xml.StartElement:
			sc.start(t)
		case xml.EndElement:
			sc.end(before)
		}
	}
	return sc.macros
}

// macroScan is one run of the scan. It is a struct rather than a closure over
// locals because the element stack, the macros found so far and the parameter
// currently being read all have to be reachable from each token's handler.
type macroScan struct {
	storage string
	decoder *xml.Decoder
	macros  []Macro
	// stack mirrors the open elements, so a parameter is attributed to the
	// macro it is a direct child of rather than to an enclosing one.
	stack []openElement
	// text accumulates the parameter currently being read. It is held here
	// rather than on the stack entry because only one parameter is ever open:
	// an ac:parameter holds flat text.
	text strings.Builder
}

// openElement is one entry of the scan's element stack: which macro it is, or
// which of that macro's parameters it opened.
type openElement struct {
	macro bool
	// param indexes macros[macroIndex].Params, or is -1 for an element that
	// opened no parameter.
	macroIndex int
	param      int
}

// start records a start element, appending a macro or a parameter as the
// element calls for.
func (sc *macroScan) start(t xml.StartElement) {
	el := openElement{param: -1}
	defer func() { sc.stack = append(sc.stack, el) }()

	if strings.ToLower(t.Name.Space) != "ac" {
		return
	}
	switch strings.ToLower(t.Name.Local) {
	case "structured-macro":
		sc.macros = append(sc.macros, Macro{Name: attrValue(t, "name"), LocalID: attrValue(t, "local-id")})
		el.macro = true
	case "parameter":
		// Only a direct child counts, matching what the Markdown converter
		// reads: an ac:parameter deeper inside a macro's body belongs to
		// whatever holds it, not to the macro.
		if len(sc.stack) == 0 || !sc.stack[len(sc.stack)-1].macro {
			return
		}
		// After the start element, InputOffset is the byte just past its ">".
		start := int(sc.decoder.InputOffset())
		macro := &sc.macros[len(sc.macros)-1]
		macro.Params = append(macro.Params, MacroParam{
			Name:  attrValue(t, "name"),
			Start: start,
			End:   start,
			// A self-closing tag ends "/>" and holds nothing, so the range
			// above is not inside the element. XML spells an empty element
			// tag exactly one way, which is what makes this readable off the
			// two bytes behind the offset.
			Empty: start >= 2 && sc.storage[start-2:start] == "/>",
		})

		el.macroIndex = len(sc.macros) - 1
		el.param = len(macro.Params) - 1
		sc.text.Reset()
	}
}

// charData accumulates the run into the open parameter, if there is one. An
// ac:parameter holds flat text, so the open one is always the innermost
// element.
func (sc *macroScan) charData(data xml.CharData) {
	if len(sc.stack) > 0 && sc.stack[len(sc.stack)-1].param >= 0 {
		sc.text.Write(data)
	}
}

// end closes the innermost element, finishing the parameter it opened. end is
// the offset the content stopped at.
func (sc *macroScan) end(end int) {
	if len(sc.stack) == 0 {
		// A close with nothing open: non-strict parsing recovering from stray
		// markup. Nothing to attribute it to.
		return
	}
	el := sc.stack[len(sc.stack)-1]
	sc.stack = sc.stack[:len(sc.stack)-1]
	if el.param < 0 {
		return
	}

	p := &sc.macros[el.macroIndex].Params[el.param]
	// A synthesized close -- non-strict parsing recovering from an unclosed
	// tag -- can land before the content started. There is no range to splice
	// then, so the parameter is marked as having none rather than carrying an
	// inverted one.
	if end < p.Start {
		p.Empty = true
		return
	}
	p.End = end
	p.Value = strings.TrimSpace(sc.text.String())
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
