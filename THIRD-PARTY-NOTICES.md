# Third-party notices

Praetor is licensed under the EUPL-1.2 ([`LICENSE`](LICENSE)). Its release archives and the
container image `ghcr.io/cordanallm/praetor` also contain code that others wrote. This file
names each of those components with its version, its license and the copyright line its
upstream states, and carries the upstream license and notice texts the licenses require to
travel with a binary.

Every release archive carries this file, `LICENSE` and the `LICENSES/` directory, which
holds the full texts of the licenses named here that apply to shipped code. The container
image carries the same files under `/usr/local/share/praetor/`. Each archive also has a
CycloneDX and an SPDX SBOM that Syft writes during the release (`.goreleaser.yaml`, `sboms`).

The component tables are generated. `praetorctl sbom notices` (`make third-party-notices`)
rewrites their rows from `go.mod`, the root `Dockerfile`, the embedded npm lock
`tools/markdownlint/package-lock.json`, the interfig pin
`tools/figures/third_party/interfig/vendor.json`, and the embedded figure player's
`tools/figures/dist/THIRD-PARTY-LICENSES.txt` with the lock the player is built from,
`tools/figures/package-lock.json`, and the embedded devcontainer CLI lock
`internal/devcontainer/cli/package-lock.json`. It keeps the copyright line each row already states
(`internal/supplychain/notices.go`). A component with no row yet, or a license outside the
reviewed set, stops the command until its row is written from the upstream license file.
`internal/supplychain/notices_test.go` fails when this file is not what the command writes,
and when the upstream license or notice text of a Go module or of a part of the figure player
is missing here verbatim.

The binaries also embed the documentation figure engine (`tools/figures/assets.go`): the
vendored interfig render source and the committed figure player, whose `player.js` bundles
interfig with React, react-dom and scheduler. Two tables list them, and
[Credits & Acknowledgements](docs/credits.md) credits them, as it credits every other project
praetor builds with, runs in CI or integrates with, from the curated list `docs/credits.yaml`.

<!-- REUSE-IgnoreStart -->

## Go standard library and runtime

Every binary statically links the Go standard library and runtime from the toolchain version
`go.mod` names.

| Component | Version | License | Copyright |
| :-- | :-- | :-- | :-- |
| Go standard library and runtime | 1.27 | BSD-3-Clause | `Copyright 2009 The Go Authors.` |

<!-- REUSE-IgnoreEnd -->

The Go license, verbatim from `LICENSE` in the Go source tree:

<!-- SPDX-SnippetBegin -->
<!-- SPDX-License-Identifier: BSD-3-Clause -->
<!-- SPDX-SnippetCopyrightText: 2009 The Go Authors. -->

```text
Copyright 2009 The Go Authors.

Redistribution and use in source and binary forms, with or without
modification, are permitted provided that the following conditions are
met:

   * Redistributions of source code must retain the above copyright
notice, this list of conditions and the following disclaimer.
   * Redistributions in binary form must reproduce the above
copyright notice, this list of conditions and the following disclaimer
in the documentation and/or other materials provided with the
distribution.
   * Neither the name of Google LLC nor the names of its
contributors may be used to endorse or promote products derived from
this software without specific prior written permission.

THIS SOFTWARE IS PROVIDED BY THE COPYRIGHT HOLDERS AND CONTRIBUTORS
"AS IS" AND ANY EXPRESS OR IMPLIED WARRANTIES, INCLUDING, BUT NOT
LIMITED TO, THE IMPLIED WARRANTIES OF MERCHANTABILITY AND FITNESS FOR
A PARTICULAR PURPOSE ARE DISCLAIMED. IN NO EVENT SHALL THE COPYRIGHT
OWNER OR CONTRIBUTORS BE LIABLE FOR ANY DIRECT, INDIRECT, INCIDENTAL,
SPECIAL, EXEMPLARY, OR CONSEQUENTIAL DAMAGES (INCLUDING, BUT NOT
LIMITED TO, PROCUREMENT OF SUBSTITUTE GOODS OR SERVICES; LOSS OF USE,
DATA, OR PROFITS; OR BUSINESS INTERRUPTION) HOWEVER CAUSED AND ON ANY
THEORY OF LIABILITY, WHETHER IN CONTRACT, STRICT LIABILITY, OR TORT
(INCLUDING NEGLIGENCE OR OTHERWISE) ARISING IN ANY WAY OUT OF THE USE
OF THIS SOFTWARE, EVEN IF ADVISED OF THE POSSIBILITY OF SUCH DAMAGE.
```

<!-- SPDX-SnippetEnd -->

<!-- REUSE-IgnoreStart -->

## Go modules

The binaries link every module `go.mod` requires.

| Module | Version | License | Copyright |
| :-- | :-- | :-- | :-- |
| `gopkg.in/yaml.v3` | v3.0.1 | MIT AND Apache-2.0 | `Copyright (c) 2006-2010 Kirill Simonov`, `Copyright (c) 2006-2011 Kirill Simonov`, `Copyright (c) 2011-2019 Canonical Ltd` |

<!-- REUSE-IgnoreEnd -->

### gopkg.in/yaml.v3 license

Verbatim from `LICENSE` in the module. The MIT text is also in
[`LICENSES/MIT.txt`](LICENSES/MIT.txt) and the Apache License 2.0 in
[`LICENSES/Apache-2.0.txt`](LICENSES/Apache-2.0.txt).

<!-- SPDX-SnippetBegin -->
<!-- SPDX-License-Identifier: MIT AND Apache-2.0 -->
<!-- SPDX-SnippetCopyrightText: 2006-2011 Kirill Simonov -->
<!-- SPDX-SnippetCopyrightText: 2011-2019 Canonical Ltd -->

```text

This project is covered by two different licenses: MIT and Apache.

#### MIT License ####

The following files were ported to Go from C files of libyaml, and thus
are still covered by their original MIT license, with the additional
copyright staring in 2011 when the project was ported over:

    apic.go emitterc.go parserc.go readerc.go scannerc.go
    writerc.go yamlh.go yamlprivateh.go

Copyright (c) 2006-2010 Kirill Simonov
Copyright (c) 2006-2011 Kirill Simonov

Permission is hereby granted, free of charge, to any person obtaining a copy of
this software and associated documentation files (the "Software"), to deal in
the Software without restriction, including without limitation the rights to
use, copy, modify, merge, publish, distribute, sublicense, and/or sell copies
of the Software, and to permit persons to whom the Software is furnished to do
so, subject to the following conditions:

The above copyright notice and this permission notice shall be included in all
copies or substantial portions of the Software.

THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR
IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY,
FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE
AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER
LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM,
OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN THE
SOFTWARE.

### Apache License ###

All the remaining project files are covered by the Apache license:

Copyright (c) 2011-2019 Canonical Ltd

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
```

<!-- SPDX-SnippetEnd -->

### gopkg.in/yaml.v3 NOTICE

Verbatim from `NOTICE` in the module, as section 4(d) of the Apache License 2.0 requires:

<!-- SPDX-SnippetBegin -->
<!-- SPDX-License-Identifier: Apache-2.0 -->
<!-- SPDX-SnippetCopyrightText: 2011-2016 Canonical Ltd. -->

```text
Copyright 2011-2016 Canonical Ltd.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
```

<!-- SPDX-SnippetEnd -->

<!-- REUSE-IgnoreStart -->

## Container base image

The image is built on distroless. The root `Dockerfile` pins the tag below to a digest.

| Image | Tag | License | Copyright |
| :-- | :-- | :-- | :-- |
| `gcr.io/distroless/static-debian13` | nonroot | Apache-2.0 | none stated in the upstream `LICENSE` |

Distroless images are assembled from Debian packages. The image keeps each package's
copyright file at `/usr/share/doc/<package>/copyright` and its package record under
`/var/lib/dpkg/status.d/`. The Apache License 2.0 text is in
[`LICENSES/Apache-2.0.txt`](LICENSES/Apache-2.0.txt).

## npm packages of the Markdown gate

The binaries embed `tools/markdownlint/package.json` and its `package-lock.json` and write
them into an adopting repository, where `npm ci` installs the packages below from the npm
registry. The binaries contain the manifest and the lock, not the packages' code, and each
installed package brings its own license file. They are listed so an adopter can see what
the emitted gate installs. The table covers every runtime entry of the lock; licenses are
as the lock records them, copyright lines as each package's license file states them.

| Package | Version | License | Copyright |
| :-- | :-- | :-- | :-- |
| `@types/debug` | 4.1.13 | MIT | `Copyright (c) Microsoft Corporation.` |
| `@types/estree` | 1.0.9 | MIT | `Copyright (c) Microsoft Corporation.` |
| `@types/estree-jsx` | 1.0.5 | MIT | `Copyright (c) Microsoft Corporation.` |
| `@types/katex` | 0.16.8 | MIT | `Copyright (c) Microsoft Corporation.` |
| `@types/ms` | 2.1.0 | MIT | `Copyright (c) Microsoft Corporation.` |
| `@types/unist` | 2.0.11 | MIT | `Copyright (c) Microsoft Corporation.` |
| `@types/unist` | 3.0.3 | MIT | `Copyright (c) Microsoft Corporation.` |
| `acorn` | 8.18.0 | MIT | `Copyright (C) 2012-2022 by various contributors (see AUTHORS)` |
| `acorn-jsx` | 5.3.2 | MIT | `Copyright (C) 2012-2017 by Ingvar Stepanyan` |
| `ansi-regex` | 6.4.0 | MIT | `Copyright (c) Sindre Sorhus <sindresorhus@gmail.com> (https://sindresorhus.com)` |
| `argparse` | 2.0.1 | Python-2.0 | `Copyright (c) 2001, 2002, 2003, 2004, 2005, 2006, 2007, 2008, 2009, 2010, 2011, 2012, 2013, 2014, 2015, 2016, 2017, 2018, 2019, 2020 Python Software Foundation; All Rights Reserved` |
| `character-entities` | 2.0.2 | MIT | `Copyright (c) 2015 Titus Wormer <tituswormer@gmail.com>` |
| `character-entities-legacy` | 3.0.0 | MIT | `Copyright (c) 2015 Titus Wormer <tituswormer@gmail.com>` |
| `character-reference-invalid` | 2.0.1 | MIT | `Copyright (c) 2015 Titus Wormer <tituswormer@gmail.com>` |
| `commander` | 15.0.0 | MIT | `Copyright (c) 2011 TJ Holowaychuk <tj@vision-media.ca>` |
| `debug` | 4.4.3 | MIT | `Copyright (c) 2014-2017 TJ Holowaychuk <tj@vision-media.ca>`, `Copyright (c) 2018-2021 Josh Junon` |
| `decode-named-character-reference` | 1.3.0 | MIT | `Copyright (c) Titus Wormer <tituswormer@gmail.com>` |
| `dequal` | 2.0.3 | MIT | `Copyright (c) Luke Edwards <luke.edwards05@gmail.com> (lukeed.com)` |
| `devlop` | 1.1.0 | MIT | `Copyright (c) 2023 Titus Wormer <tituswormer@gmail.com>` |
| `entities` | 8.1.0 | BSD-2-Clause | `Copyright (c) Felix Böhm` |
| `estree-util-is-identifier-name` | 3.0.0 | MIT | `Copyright (c) 2020 Titus Wormer <tituswormer@gmail.com>` |
| `estree-util-visit` | 2.0.0 | MIT | `Copyright (c) 2021 Titus Wormer <tituswormer@gmail.com>` |
| `get-east-asian-width` | 1.7.0 | MIT | `Copyright (c) Sindre Sorhus <sindresorhus@gmail.com> (https://sindresorhus.com)` |
| `is-alphabetical` | 2.0.1 | MIT | `Copyright (c) 2016 Titus Wormer <tituswormer@gmail.com>` |
| `is-alphanumerical` | 2.0.1 | MIT | `Copyright (c) 2016 Titus Wormer <tituswormer@gmail.com>` |
| `is-decimal` | 2.0.1 | MIT | `Copyright (c) 2016 Titus Wormer <tituswormer@gmail.com>` |
| `is-hexadecimal` | 2.0.1 | MIT | `Copyright (c) 2016 Titus Wormer <tituswormer@gmail.com>` |
| `js-yaml` | 5.4.2 | MIT | `Copyright (C) 2011-2015 by Vitaly Puzrin` |
| `jsonc-parser` | 3.3.1 | MIT | `Copyright (c) Microsoft` |
| `katex` | 0.19.0 | MIT | `Copyright (c) 2013-2020 Khan Academy and other contributors` |
| `markdownlint` | 0.41.1 | MIT | `Copyright (c) David Anson` |
| `micromark` | 4.0.2 | MIT | `Copyright (c) Titus Wormer <tituswormer@gmail.com>` |
| `micromark` | 4.0.3 | MIT | `Copyright (c) Titus Wormer <tituswormer@gmail.com>` |
| `micromark-core-commonmark` | 2.0.3 | MIT | `Copyright (c) Titus Wormer <tituswormer@gmail.com>` |
| `micromark-extension-directive` | 4.0.0 | MIT | `Copyright (c) Titus Wormer <tituswormer@gmail.com>` |
| `micromark-extension-gfm-autolink-literal` | 2.1.0 | MIT | `Copyright (c) 2020 Titus Wormer <tituswormer@gmail.com>` |
| `micromark-extension-gfm-footnote` | 2.1.0 | MIT | `Copyright (c) 2021 Titus Wormer <tituswormer@gmail.com>` |
| `micromark-extension-gfm-table` | 2.1.1 | MIT | `Copyright (c) Titus Wormer <tituswormer@gmail.com>` |
| `micromark-extension-math` | 3.1.0 | MIT | `Copyright (c) 2020 Titus Wormer <tituswormer@gmail.com>` |
| `micromark-extension-mdx-expression` | 3.0.1 | MIT | `Copyright (c) Titus Wormer <tituswormer@gmail.com>` |
| `micromark-extension-mdx-jsx` | 3.0.2 | MIT | `Copyright (c) 2020 Titus Wormer <tituswormer@gmail.com>` |
| `micromark-extension-mdx-md` | 2.0.0 | MIT | `Copyright (c) 2020 Titus Wormer <tituswormer@gmail.com>` |
| `micromark-extension-mdxjs` | 3.0.0 | MIT | `Copyright (c) 2020 Titus Wormer <tituswormer@gmail.com>` |
| `micromark-extension-mdxjs-esm` | 3.0.0 | MIT | `Copyright (c) 2020 Titus Wormer <tituswormer@gmail.com>` |
| `micromark-factory-destination` | 2.0.1 | MIT | `Copyright (c) Titus Wormer <tituswormer@gmail.com>` |
| `micromark-factory-label` | 2.0.1 | MIT | `Copyright (c) Titus Wormer <tituswormer@gmail.com>` |
| `micromark-factory-mdx-expression` | 2.0.3 | MIT | `Copyright (c) Titus Wormer <tituswormer@gmail.com>` |
| `micromark-factory-space` | 2.1.0 | MIT | `Copyright (c) Titus Wormer <tituswormer@gmail.com>` |
| `micromark-factory-title` | 2.0.1 | MIT | `Copyright (c) Titus Wormer <tituswormer@gmail.com>` |
| `micromark-factory-whitespace` | 2.0.1 | MIT | `Copyright (c) Titus Wormer <tituswormer@gmail.com>` |
| `micromark-util-character` | 2.1.1 | MIT | `Copyright (c) Titus Wormer <tituswormer@gmail.com>` |
| `micromark-util-chunked` | 2.0.1 | MIT | `Copyright (c) Titus Wormer <tituswormer@gmail.com>` |
| `micromark-util-classify-character` | 2.0.1 | MIT | `Copyright (c) Titus Wormer <tituswormer@gmail.com>` |
| `micromark-util-combine-extensions` | 2.0.1 | MIT | `Copyright (c) Titus Wormer <tituswormer@gmail.com>` |
| `micromark-util-decode-numeric-character-reference` | 2.0.2 | MIT | `Copyright (c) Titus Wormer <tituswormer@gmail.com>` |
| `micromark-util-edit-map` | 1.0.0 | MIT | `Copyright (c) Titus Wormer <tituswormer@gmail.com>` |
| `micromark-util-encode` | 2.0.1 | MIT | `Copyright (c) Titus Wormer <tituswormer@gmail.com>` |
| `micromark-util-events-to-acorn` | 2.0.3 | MIT | `Copyright (c) Titus Wormer <tituswormer@gmail.com>` |
| `micromark-util-html-tag-name` | 2.0.1 | MIT | `Copyright (c) Titus Wormer <tituswormer@gmail.com>` |
| `micromark-util-normalize-identifier` | 2.0.1 | MIT | `Copyright (c) Titus Wormer <tituswormer@gmail.com>` |
| `micromark-util-resolve-all` | 2.0.1 | MIT | `Copyright (c) Titus Wormer <tituswormer@gmail.com>` |
| `micromark-util-sanitize-uri` | 2.0.1 | MIT | `Copyright (c) Titus Wormer <tituswormer@gmail.com>` |
| `micromark-util-subtokenize` | 2.1.0 | MIT | `Copyright (c) Titus Wormer <tituswormer@gmail.com>` |
| `micromark-util-symbol` | 2.0.1 | MIT | `Copyright (c) Titus Wormer <tituswormer@gmail.com>` |
| `micromark-util-types` | 2.0.2 | MIT | `Copyright (c) Titus Wormer <tituswormer@gmail.com>` |
| `ms` | 2.1.3 | MIT | `Copyright (c) 2020 Vercel, Inc.` |
| `parse-entities` | 4.0.2 | MIT | `Copyright (c) Titus Wormer <mailto:tituswormer@gmail.com>` |
| `parse5` | 8.0.1 | MIT | `Copyright (c) 2013-2019 Ivan Nikulin (ifaaan@gmail.com, https://github.com/inikulin)` |
| `smol-toml` | 1.9.0 | BSD-3-Clause | `Copyright (c) Squirrel Chat et al., All rights reserved.` |
| `string-width` | 8.2.1 | MIT | `Copyright (c) Sindre Sorhus <sindresorhus@gmail.com> (https://sindresorhus.com)` |
| `strip-ansi` | 7.2.0 | MIT | `Copyright (c) Sindre Sorhus <sindresorhus@gmail.com> (https://sindresorhus.com)` |
| `unist-util-position-from-estree` | 2.0.0 | MIT | `Copyright (c) 2021 Titus Wormer <tituswormer@gmail.com>` |
| `unist-util-stringify-position` | 4.0.0 | MIT | `Copyright (c) 2016 Titus Wormer <tituswormer@gmail.com>` |
| `vfile-message` | 4.0.3 | MIT | `Copyright (c) Titus Wormer <tituswormer@gmail.com>` |

<!-- REUSE-IgnoreEnd -->

<!-- REUSE-IgnoreStart -->

## Vendored figure engine

The binaries embed the render source of interfig, vendored byte-identical at the upstream commit
`tools/figures/third_party/interfig/vendor.json` pins, with its upstream `LICENSE`
(`tools/figures/third_party/interfig/VENDOR.md`).

| Component | Commit | License | Copyright |
| :-- | :-- | :-- | :-- |
| `interfig` | ccfe85b4851957ac2adf88b4a9ddf9668b2882f1 | MIT | `Copyright (c) 2025 Vectorize AI, Inc.` |

## npm packages of the figure player

The embedded figure player `tools/figures/dist/player.js` contains the code of the packages
below, bundled by esbuild from `tools/figures/package-lock.json`. The table covers every package
the player's `THIRD-PARTY-LICENSES.txt` names beside interfig; versions and licenses are as the
lock records them, copyright lines as each package's license file states them.

| Package | Version | License | Copyright |
| :-- | :-- | :-- | :-- |
| `react` | 19.3.0 | MIT | `Copyright (c) Meta Platforms, Inc. and affiliates.` |
| `react-dom` | 19.3.0 | MIT | `Copyright (c) Meta Platforms, Inc. and affiliates.` |
| `scheduler` | 0.28.0 | MIT | `Copyright (c) Meta Platforms, Inc. and affiliates.` |

<!-- REUSE-IgnoreEnd -->

### interfig license

The interfig license, verbatim from `LICENSE` in the upstream tree
(`tools/figures/third_party/interfig/upstream/LICENSE`):

<!-- SPDX-SnippetBegin -->
<!-- SPDX-License-Identifier: MIT -->
<!-- SPDX-SnippetCopyrightText: 2025 Vectorize AI, Inc. -->

```text
MIT License

Copyright (c) 2025 Vectorize AI, Inc.

Permission is hereby granted, free of charge, to any person obtaining a copy
of this software and associated documentation files (the "Software"), to deal
in the Software without restriction, including without limitation the rights
to use, copy, modify, merge, publish, distribute, sublicense, and/or sell
copies of the Software, and to permit persons to whom the Software is
furnished to do so, subject to the following conditions:

The above copyright notice and this permission notice shall be included in all
copies or substantial portions of the Software.

THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR
IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY,
FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE
AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER
LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM,
OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN THE
SOFTWARE.
```

<!-- SPDX-SnippetEnd -->

### React, react-dom and scheduler license

React, react-dom and scheduler carry the same license, verbatim from each package's `LICENSE`:

<!-- SPDX-SnippetBegin -->
<!-- SPDX-License-Identifier: MIT -->
<!-- SPDX-SnippetCopyrightText: Meta Platforms, Inc. and affiliates. -->

```text
MIT License

Copyright (c) Meta Platforms, Inc. and affiliates.

Permission is hereby granted, free of charge, to any person obtaining a copy
of this software and associated documentation files (the "Software"), to deal
in the Software without restriction, including without limitation the rights
to use, copy, modify, merge, publish, distribute, sublicense, and/or sell
copies of the Software, and to permit persons to whom the Software is
furnished to do so, subject to the following conditions:

The above copyright notice and this permission notice shall be included in all
copies or substantial portions of the Software.

THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR
IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY,
FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE
AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER
LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM,
OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN THE
SOFTWARE.
```

<!-- SPDX-SnippetEnd -->

<!-- REUSE-IgnoreStart -->

## npm packages of the devcontainer CLI

The binaries embed `internal/devcontainer/cli/package.json` and its `package-lock.json`
(`internal/devcontainer/cli.go`). Before a devcontainer build, praetorctl downloads the Node.js
release `internal/devcontainer/cli/node.json` pins from nodejs.org, refuses it unless its
SHA-256 is the pinned one, and runs that release's npm with `npm ci` to install the package
below from the npm registry into its tool cache. The binaries contain the manifest, the lock and
the pins, not the code of the CLI or of Node.js; the installed package and the Node.js archive
each bring their own license files. The table covers every runtime entry of the lock; licenses
are as the lock records them, copyright lines as each package's license file states them.

| Package | Version | License | Copyright |
| :-- | :-- | :-- | :-- |
| `@devcontainers/cli` | 0.89.0 | MIT | `Copyright (c) Microsoft Corporation.` |

<!-- REUSE-IgnoreEnd -->

## Vendored client schemas

The binaries embed vendored client configuration and hook schemas under
`internal/clientschema/upstream/` (`internal/clientschema/clientschema.go`).
Generated type definitions for OpenAI Codex hooks (`internal/codexhook/events_gen.go`)
carry types and comments derived from the vendored Codex schemas.

### OpenAI Codex NOTICE

Verbatim from `NOTICE` in OpenAI Codex (`openai/codex`), as section 4(d) of the Apache License 2.0 requires:

<!-- SPDX-SnippetBegin -->
<!-- SPDX-License-Identifier: Apache-2.0 -->
<!-- SPDX-SnippetCopyrightText: 2025 OpenAI -->

```text
OpenAI Codex
Copyright 2025 OpenAI

This project includes code derived from [Ratatui](https://github.com/ratatui/ratatui), licensed under the MIT license.
Copyright (c) 2016-2022 Florian Dehau
Copyright (c) 2023-2025 The Ratatui Developers
```

<!-- SPDX-SnippetEnd -->
