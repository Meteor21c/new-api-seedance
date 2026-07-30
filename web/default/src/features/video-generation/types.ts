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

export const VIDEO_MODELS = [
  'cheap-seedance-2.0',
  'cheap-seedance-2.0-fast',
  'cheap-seedance-2.0-mini',
] as const

export const VIDEO_RESOLUTIONS = ['480p', '720p', '1080p', '4K'] as const

export const VIDEO_ASPECT_RATIOS = [
  '16:9',
  '9:16',
  '1:1',
  '4:3',
  '3:4',
  '21:9',
] as const

export const VIDEO_MODES = ['text_with_reference', 'start_end_frame'] as const

export type VideoModel = (typeof VIDEO_MODELS)[number]
export type VideoResolution = (typeof VIDEO_RESOLUTIONS)[number]
export type VideoAspectRatio = (typeof VIDEO_ASPECT_RATIOS)[number]
export type VideoMode = (typeof VIDEO_MODES)[number]

export interface VideoGenerationRequest {
  model: VideoModel
  prompt: string
  duration: number
  resolution: VideoResolution
  aspect_ratio: VideoAspectRatio
  mode: VideoMode
  audio: boolean
  reference_images?: string[]
  start_image_url?: string
  end_image_url?: string
}

export interface VideoCreateResponse {
  id: string
  task_id: string
  model?: string
  status?: string
}

export interface VideoTask {
  id: number
  created_at: number
  updated_at: number
  task_id: string
  platform: string
  status: string
  progress: string
  fail_reason: string
  result_url?: string
  quota: number
  submit_time: number
  properties?: {
    input?: string
    origin_model_name?: string
  }
}

export interface VideoTaskResponse {
  code: string
  message?: string
  data: VideoTask
}

export interface VideoTaskListResponse {
  success: boolean
  message?: string
  data?: {
    items?: VideoTask[]
    total?: number
  }
}
