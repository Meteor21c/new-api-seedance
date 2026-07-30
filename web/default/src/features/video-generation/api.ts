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

import type {
  VideoCreateResponse,
  VideoGenerationRequest,
  VideoTaskListResponse,
  VideoTaskResponse,
} from './types'

export async function createVideo(
  request: VideoGenerationRequest
): Promise<VideoCreateResponse> {
  const response = await api.post<VideoCreateResponse>(
    '/pg/video/generations',
    request
  )
  return response.data
}

export async function getVideoTask(taskId: string): Promise<VideoTaskResponse> {
  const response = await api.get<VideoTaskResponse>(
    `/pg/video/generations/${encodeURIComponent(taskId)}`,
    { disableDuplicate: true }
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
