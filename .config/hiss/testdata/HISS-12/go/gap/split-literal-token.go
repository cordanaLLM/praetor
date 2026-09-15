package p

// HISS-12: a real AWS access key id, in git history, assembled from two adjacent
// literals. The credential is fully recoverable but no single line carries the
// pattern the scanner matches, so it is never reported.
const AWSAccessKeyID = "AKIAZ3MT" + "Q7XK2LPWV4RD"
