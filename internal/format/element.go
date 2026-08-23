package format

import "encoding/xml"

// ElementEnds reports where each element carrying the given local-id ends: the
// byte just past its end tag, which is where a sibling inserted after it
// starts. The offsets index the string that was passed in, in document order.
//
// Confluence stamps local-id on block elements (<p>, headings, …) and
// ac:local-id on macros. Those are the same attribute under different
// prefixes, and the attribute is read by local name — the way every other
// attribute in this package is read — so either one is a usable anchor.
//
// Every match is reported rather than the first: a body should not carry the
// same id twice, but acting on the first of two would insert somewhere the
// caller never named. Deciding what to do with none or several is the caller's
// business, the way it is for Macros.
func ElementEnds(storage, localID string) []int {
	// An absent attribute reads as the empty string, so an empty id would
	// match every element in the body rather than none of them.
	if localID == "" {
		return nil
	}

	decoder := newStorageDecoder(storage)
	var (
		ends []int
		// stack mirrors the open elements, marking which of them carry the
		// id, so an end tag is attributed to the element that opened it.
		stack []bool
	)
	for {
		token, err := decoder.Token()
		if err != nil {
			// EOF, or a document too broken to recover from. Either way,
			// report what was found, the way Macros does.
			break
		}

		switch t := token.(type) {
		case xml.StartElement:
			stack = append(stack, attrValue(t, "local-id") == localID)
		case xml.EndElement:
			if len(stack) == 0 {
				// A close with nothing open: non-strict parsing recovering
				// from stray markup. Nothing to attribute it to.
				continue
			}
			matched := stack[len(stack)-1]
			stack = stack[:len(stack)-1]
			if matched {
				// After an end element, InputOffset is the byte just past its
				// ">". A self-closing element closes at the same offset its
				// start element ended at, so its "/>" is covered too.
				ends = append(ends, int(decoder.InputOffset()))
			}
		}
	}
	return ends
}
