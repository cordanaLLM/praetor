#!/bin/sh
# SPDX-FileCopyrightText: 2026 Lusoris <lusoris@pm.me>
# SPDX-License-Identifier: EUPL-1.2
set -eu
exec sh .config/lefthook/engine.sh forge check-commits --pre-push
