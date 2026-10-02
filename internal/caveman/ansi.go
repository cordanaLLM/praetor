package caveman

import "regexp"

// ansiRe matches the escape sequences a terminal acts on instead of printing (ECMA-48):
//
//   - a CSI sequence (colours, cursor moves): ESC [, parameter bytes 0 to ?, intermediate
//     bytes blank to /, one final byte @ to ~;
//   - an OSC string (titles, hyperlinks): ESC ], a body, then BEL or ST (ESC \). The body
//     holds no BEL, no ESC and no line end, so the string closes on the line that opens it;
//   - the two-byte escapes ESC @ to ESC _, ESC [ apart.
var ansiRe = regexp.MustCompile(`\x1b\[[0-?]*[ -/]*[@-~]|\x1b\][^\x07\x1b\n]*(?:\x07|\x1b\\)|\x1b[@-Z\\-_]`)

// stripANSI removes the escape sequences of text, zero-width, and nothing else. It is the one
// ANSI reader of the package: Compress and Floor both read their text through it (#713), so
// what Compress removes is what Floor does not count.
//
// An escape left unterminated on its line is no sequence, and its digits and words stay
// text. A CSI without a final byte stays whole. An OSC without BEL or ST on its line keeps
// its body and loses only its introducer, ESC ], which is a two-byte escape of its own. A
// terminator on a later line closes nothing: an OSC body that crossed line ends would take
// every line between with it, out of the output of Compress and out of the facts of Floor.
//
// A CSI is read as ECMA-48 defines it, a blank being an intermediate byte ("ESC [ 2 SP q"
// sets the cursor shape). A colour code cut after its parameters and followed by a blank and
// a letter is therefore a whole sequence, that letter its final byte: of "ESC [ 38;5 MUST"
// the text is "UST", as a terminal prints it.
func stripANSI(text string) string {
	return ansiRe.ReplaceAllString(text, "")
}
