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

type GenerationModel = {
  id: string
}

type GenerationModelsResponse = {
  success?: boolean
  data?: GenerationModel[]
  fallback?: boolean
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
  const response = await api.get<GenerationModelsResponse>(
    '/api/user/generation_models',
    {
      params: { type: 'image' },
      skipErrorHandler: true,
    }
  )
  return (response.data.data ?? []).map((model) => model.id)
}
