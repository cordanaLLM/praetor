package p

import "encoding/base64"

// HISS-12: a GitHub personal access token base64-wrapped before it was committed.
// gitleaks decodes it (--max-decode-depth defaults to 5) and reports github-pat with
// tag decoded:base64, so hiding a credential behind an encoding does not work.
const encodedToken = "Z2hwXzAxNkM3RDIzNDVCNkU3ODlGMDEyMzQ1Njc4OUFCQ0RFRjAxMg=="

func Token() (string, error) {
	raw, err := base64.StdEncoding.DecodeString(encodedToken)
	if err != nil {
		return "", err
	}
	return string(raw), nil
}
