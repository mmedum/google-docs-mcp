package doc

import "unicode/utf16"

// UTF16Len returns the length of s in UTF-16 code units, the unit the
// Docs API counts indices in. Characters outside the BMP (most emoji)
// count as two.
func UTF16Len(s string) int64 {
	var n int64
	for _, r := range s {
		n += int64(utf16.RuneLen(r))
	}
	return n
}

// CodePointToUTF16 converts an offset in code points to UTF-16 units.
// Offsets past the end clamp to the total length.
func CodePointToUTF16(s string, cp int) int64 {
	var n int64
	i := 0
	for _, r := range s {
		if i >= cp {
			break
		}
		n += int64(utf16.RuneLen(r))
		i++
	}
	return n
}

// UTF16ToCodePoint converts a UTF-16 offset to code points. An offset
// that lands inside a surrogate pair rounds up to the next character.
func UTF16ToCodePoint(s string, u int64) int {
	var n int64
	i := 0
	for _, r := range s {
		if n >= u {
			break
		}
		n += int64(utf16.RuneLen(r))
		i++
	}
	return i
}

// UTF16ToByte converts a UTF-16 offset into a byte offset in s.
func UTF16ToByte(s string, u int64) int {
	var n int64
	for i, r := range s {
		if n >= u {
			return i
		}
		n += int64(utf16.RuneLen(r))
	}
	return len(s)
}
