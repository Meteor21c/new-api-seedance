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

import { api } from '@/lib/api'

export type ImageGenerationRequest = {
  model: string
  prompt: string
  n: number
  size?: string
  quality?: string
  response_format: 'url'
}

export type GeneratedImage = {
  url?: string
  b64_json?: string
  revised_prompt?: string
}

export type ImageGenerationResponse = {
  created?: number
  data: GeneratedImage[]
}

type PricingModel = {
  model_name?: string
  supported_endpoint_types?: string[]
}

type PricingResponse = {
  success?: boolean
  data?: PricingModel[]
}

export async function createImage(
  request: ImageGenerationRequest
): Promise<ImageGenerationResponse> {
  const response = await api.post<ImageGenerationResponse>(
    '/pg/images/generations',
    request,
    { skipErrorHandler: true }
  )
  return response.data
}

export async function getImageModels(): Promise<string[]> {
  const response = await api.get<PricingResponse>('/api/pricing', {
    skipErrorHandler: true,
  })
  const models = response.data.data ?? []
  return [
    ...new Set(
      models
        .filter((model) =>
          model.supported_endpoint_types?.includes('image-generation')
        )
        .map((model) => model.model_name?.trim() ?? '')
        .filter(Boolean)
    ),
  ].sort((left, right) => left.localeCompare(right))
}
