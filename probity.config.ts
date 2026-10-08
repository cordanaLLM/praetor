// SPDX-FileCopyrightText: 2026 Lusoris <lusoris@proton.me>
//
// SPDX-License-Identifier: EUPL-1.2

import { createRequire } from 'node:module'
import { defineConfig, enforceTdd } from '@nizos/probity'
import type { Agent, Verdict } from '@nizos/probity'

function createHaikuAgent(): Agent | undefined {
  try {
    const probityEntry = import.meta.resolve('@nizos/probity')
    const req = createRequire(probityEntry)
    const sdkPath = req.resolve('@anthropic-ai/claude-agent-sdk')
    const toVerdictPath = req.resolve('./vendors/to-verdict.js')

    return {
      async reason(prompt: string): Promise<Verdict> {
        const [{ query }, { toVerdict }] = await Promise.all([
          import(sdkPath),
          import(toVerdictPath),
        ])

        return toVerdict(async () => {
          for await (const message of query({
            prompt,
            options: {
              model: 'claude-haiku-5-5',
              maxTurns: 1,
              thinking: { type: 'disabled' },
              permissionMode: 'dontAsk',
              tools: [],
              settings: { autoMemoryEnabled: false },
              settingSources: [],
              persistSession: false,
            },
          })) {
            if (message.type === 'result' && message.subtype === 'success') {
              if (typeof message.result !== 'string') {
                throw new Error(
                  `expected string result from validator, got ${typeof message.result}`,
                )
              }
              const meta = message.modelUsage ? { modelUsage: message.modelUsage } : undefined
              return meta ? { text: message.result, meta } : { text: message.result }
            }
          }
          throw new Error('no result message received from Claude Agent SDK query')
        })
      },
    }
  } catch {
    return undefined
  }
}

const haikuAgent = createHaikuAgent()

export default defineConfig({
  ...(haikuAgent ? { ai: haikuAgent } : {}),
  rules: [
    {
      files: ['internal/**/*.go', 'cmd/**/*.go', 'tools/**/*.go', '!**/testdata/**'],
      rules: [enforceTdd()],
    },
  ],
})
