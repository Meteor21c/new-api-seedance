/*
Copyright (C) 2023-2026 QuantumNous

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as
published by the Free Software Foundation, either version 3 of the
License, or (at your option) any later version.

This program is distributed in the hope that it will be useful,
but WITHOUT ANY WARRANTY; without even the implied warranty of
MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE. See the
GNU Affero General Public License for more details.

You should have received a copy of the GNU Affero General Public License
along with this program. If not, see <https://www.gnu.org/licenses/>.

For commercial licensing, please contact support@quantumnous.com
*/

import assert from 'node:assert/strict'
import { describe, test } from 'node:test'

import { buildImageMcpPrompt, imageMcpEndpoint } from '../mcp-prompt'

describe('image MCP prompt', () => {
  test('uses the dedicated image endpoint and current form values', () => {
    const prompt = buildImageMcpPrompt(
      {
        model: 'gpt-image-2',
        prompt: '一只戴耳机的猫',
        n: 2,
        size: '1024x1024',
        quality: 'high',
      },
      [],
      imageMcpEndpoint('https://api.example.com/')
    )

    assert.match(prompt, /meteor-image MCP/)
    assert.match(prompt, /https:\/\/api\.example\.com\/mcp\/image/)
    assert.match(prompt, /模型：gpt-image-2/)
    assert.match(prompt, /图片数量：2/)
    assert.match(prompt, /data\[\]\.b64_json/)
    assert.doesNotMatch(prompt, /meteor-video MCP 生成图片/)
  })

  test('warns that local reference bytes are not embedded in copied text', () => {
    const prompt = buildImageMcpPrompt(
      {
        model: 'gpt-image-2',
        prompt: '参考附件修改配色',
        n: 1,
        size: 'auto',
        quality: 'auto',
      },
      [new File(['image'], 'reference.png', { type: 'image/png' })],
      imageMcpEndpoint('https://api.example.com')
    )

    assert.match(prompt, /本地文件：reference\.png/)
    assert.match(prompt, /复制内容不包含本地文件字节/)
    assert.match(prompt, /当前 meteor-image MCP 只支持文生图/)
  })
})
