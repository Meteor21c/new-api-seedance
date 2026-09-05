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

import { fetchUpstreamModels, getChannels } from '@/features/channels/api'
import type { Channel } from '@/features/channels/types'
import { api } from '@/lib/api'

import type { ChannelDebugRequest, ChannelDebugResponse } from './types'

const CHANNEL_PAGE_SIZE = 100

export async function getAllDebugChannels(): Promise<Channel[]> {
  const channels: Channel[] = []
  let page = 1
  let total = Number.POSITIVE_INFINITY

  while (channels.length < total) {
    const response = await getChannels({
      p: page,
      page_size: CHANNEL_PAGE_SIZE,
      id_sort: true,
    })
    if (!response.success) {
      throw new Error(response.message || 'Failed to load channels')
    }
    const items = response.data?.items ?? []
    total = response.data?.total ?? items.length
    channels.push(...items)
    if (items.length === 0 || items.length < CHANNEL_PAGE_SIZE) break
    page += 1
  }

  return channels
}

export async function getChannelDebugModels(channelId: number) {
  const response = await fetchUpstreamModels(channelId)
  if (!response.success) {
    throw new Error(response.message || 'Failed to load upstream models')
  }
  return response.data ?? []
}

export async function runChannelDebug(
  channelId: number,
  request: ChannelDebugRequest
): Promise<ChannelDebugResponse> {
  const response = await api.post<ChannelDebugResponse>(
    `/api/channel/debug/${channelId}`,
    request,
    {
      skipBusinessError: true,
      skipErrorHandler: true,
      disableDuplicate: true,
    }
  )
  return response.data
}
