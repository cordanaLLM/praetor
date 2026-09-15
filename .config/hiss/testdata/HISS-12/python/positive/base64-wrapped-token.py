"""HISS-12: a GitHub personal access token base64-wrapped before it was committed.
gitleaks decodes it and reports github-pat with tag decoded:base64."""

import base64

ENCODED_TOKEN = "Z2hwXzAxNkM3RDIzNDVCNkU3ODlGMDEyMzQ1Njc4OUFCQ0RFRjAxMg=="


def token() -> str:
    return base64.b64decode(ENCODED_TOKEN).decode("ascii")
