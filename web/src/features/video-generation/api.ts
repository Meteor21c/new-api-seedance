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

import { writeGenerationRecord } from '../generation-storage'
import type {
  MaterialUploadResponse,
  GenerationModelsResponse,
  VideoCreateResponse,
  VideoGenerationRequest,
  VideoTaskListResponse,
  VideoTaskResponse,
} from './types'

export async function getVideoModels() {
  const response = await api.get<GenerationModelsResponse>(
    '/api/user/generation_models',
    {
      params: { type: 'video' },
      skipErrorHandler: true,
    }
  )
  return response.data.data ?? []
}

function materialContentType(file: File): string {
  if (file.type) return file.type
  const extension = file.name.split('.').pop()?.toLowerCase()
  if (extension === 'jpg' || extension === 'jpeg') return 'image/jpeg'
  if (extension === 'png') return 'image/png'
  if (extension === 'webp') return 'image/webp'
  return ''
}

export async function uploadMaterial(file: File): Promise<string> {
  const contentType = materialContentType(file)
  const response = await api.post<MaterialUploadResponse>(
    '/pg/materials/uploads',
    {
      file_name: file.name,
      content_type: contentType,
      size_bytes: file.size,
    },
    { skipErrorHandler: true }
  )
  const upload = response.data
  const headers = new Headers()
  Object.entries(upload.headers ?? {}).forEach(([name, value]) => {
    if (name.toLowerCase() !== 'content-length') {
      headers.set(name, value)
    }
  })
  const uploadResponse = await fetch(upload.upload_url, {
    method: upload.method || 'PUT',
    headers,
    body: file,
  })
  if (!uploadResponse.ok) {
    throw new Error(`OSS upload failed with HTTP ${uploadResponse.status}`)
  }
  return upload.material_id
}

export async function createVideo(
  request: VideoGenerationRequest
): Promise<VideoCreateResponse> {
  const response = await api.post<VideoCreateResponse>(
    '/pg/video/generations',
    request,
    { skipErrorHandler: true }
  )
  return response.data
}

/**
 * Persist the asynchronous task id outside the route component. This keeps
 * video polling recoverable when the user changes sections while the request
 * is being submitted.
 */
export async function createVideoTracked(
  request: VideoGenerationRequest
): Promise<VideoCreateResponse> {
  const response = await createVideo(request)
  const taskId = response.task_id || response.id
  if (taskId) writeGenerationRecord('video-current-task-id', taskId)
  return response
}

export async function getVideoTask(taskId: string): Promise<VideoTaskResponse> {
  const response = await api.get<VideoTaskResponse>(
    `/pg/video/generations/${encodeURIComponent(taskId)}`,
    { disableDuplicate: true }
  )
  return response.data
}

/**
 * Load completed video bytes through the authenticated New API proxy.
 *
 * A native <video src="..."> request cannot attach the dashboard Bearer
 * token, so the generation page must fetch the protected content with the
 * configured API client and hand the player a local object URL instead.
 */
export async function getVideoContent(
  taskId: string,
  signal?: AbortSignal
): Promise<Blob> {
  const response = await api.get<Blob>(
    `/v1/videos/${encodeURIComponent(taskId)}/content`,
    {
      responseType: 'blob',
      signal,
      disableDuplicate: true,
      skipBusinessError: true,
      skipErrorHandler: true,
    }
  )
  return response.data
}

export async function getVideoTasks(): Promise<VideoTaskListResponse> {
  const response = await api.get<VideoTaskListResponse>(
    '/api/task/self?p=1&page_size=12&platform=59',
    { disableDuplicate: true }
  )
  return response.data
}
