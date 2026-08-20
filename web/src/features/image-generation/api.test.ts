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

import { describe, expect, test } from 'vitest'

import { normalizeGeneratedImage } from './normalization'

function wrappedImageURL(dataURL: string): string {
  const envelope = JSON.stringify({ kind: 'upstream', url: dataURL })
  const bytes = new TextEncoder().encode(envelope)
  let binary = ''
  bytes.forEach((byte) => {
    binary += String.fromCharCode(byte)
  })
  const token = globalThis
    .btoa(binary)
    .replaceAll('+', '-')
    .replaceAll('/', '_')
    .replace(/=+$/, '')
  return `https://multimodal.example/v1/images/content/${token}.signature`
}

describe('image response normalization', () => {
  test('turns wrapped inline image URLs into reusable Base64 data', () => {
    expect(
      normalizeGeneratedImage({
        url: wrappedImageURL('data:image/png;base64,aGVsbG8='),
        revised_prompt: 'draw a cat',
      })
    ).toEqual({
      url: undefined,
      b64_json: 'aGVsbG8=',
      revised_prompt: 'draw a cat',
    })
  })

  test('leaves ordinary provider URLs unchanged', () => {
    const image = { url: 'https://cdn.example/image.png' }
    expect(normalizeGeneratedImage(image)).toBe(image)
  })

  test('does not replace an existing Base64 result', () => {
    const image = {
      url: wrappedImageURL('data:image/png;base64,bmV3'),
      b64_json: 'ZXhpc3Rpbmc=',
    }
    expect(normalizeGeneratedImage(image)).toBe(image)
  })
})
