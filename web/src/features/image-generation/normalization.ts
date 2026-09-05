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

type NormalizableImage = {
  url?: string
  b64_json?: string
}

type WrappedImageEnvelope = {
  kind?: string
  url?: string
}

/**
 * Decode the provider-specific /v1/images/content/<token> wrapper used by a
 * few OpenAI-compatible image providers. The token contains a JSON envelope
 * whose URL is already a data:image Base64 payload, so decoding it in the
 * browser avoids a second request to a short-lived or extension-blocked URL.
 */
export function normalizeGeneratedImage<T extends NormalizableImage>(
  image: T
): T {
  if (image.b64_json || !image.url) return image

  try {
    const parsed = new URL(image.url)
    const marker = '/v1/images/content/'
    const markerIndex = parsed.pathname.indexOf(marker)
    if (markerIndex < 0) return image

    const token = parsed.pathname.slice(markerIndex + marker.length)
    if (!token || token.includes('/')) return image
    const separatorIndex = token.indexOf('.')
    if (separatorIndex <= 0 || separatorIndex === token.length - 1) return image

    const encodedEnvelope = token.slice(0, separatorIndex)
    const normalizedBase64 = encodedEnvelope
      .replaceAll('-', '+')
      .replaceAll('_', '/')
      .padEnd(Math.ceil(encodedEnvelope.length / 4) * 4, '=')
    const binary = globalThis.atob(normalizedBase64)
    const bytes = Uint8Array.from(binary, (character) =>
      character.charCodeAt(0)
    )
    const envelope = JSON.parse(
      new TextDecoder().decode(bytes)
    ) as WrappedImageEnvelope
    if (envelope.kind !== 'upstream' || !envelope.url) return image

    const match = /^data:image\/[a-z0-9.+-]+;base64,([a-z0-9+/=\s]+)$/i.exec(
      envelope.url
    )
    if (!match?.[1]) return image

    return {
      ...image,
      url: undefined,
      b64_json: match[1].replaceAll(/\s/g, ''),
    }
  } catch {
    return image
  }
}
