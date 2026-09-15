// HISS-12: a GitHub personal access token base64-wrapped before it was committed.
// gitleaks decodes it and reports github-pat with tag decoded:base64.
const ENCODED_TOKEN = "Z2hwXzAxNkM3RDIzNDVCNkU3ODlGMDEyMzQ1Njc4OUFCQ0RFRjAxMg==";

export function token(): string {
  return atob(ENCODED_TOKEN);
}
