// Planted test fixture for CI Trivy vulnerability detection verification (HISS-20 negative case).
// Pinned known HIGH vulnerability: GHSA-4v7x-pqxf-cx7x / CVE-2023-39325 (HTTP/2 Rapid Reset in golang.org/x/net < 0.17.0).
module testdata/trivy-planted

go 1.21

require golang.org/x/net v0.16.0
