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

export type ChannelDebugMessage = {
  role: 'system' | 'user' | 'assistant'
  content: string
}

export type ChannelDebugRequest = {
  model: string
  messages: ChannelDebugMessage[]
  stream: boolean
  max_tokens: number
  endpoint_type?: '' | 'openai' | 'openai_response'
}

export type ChannelDebugResult = {
  content: string
  raw_response: string
  channel: {
    id: number
    name: string
    type: number
  }
  model: {
    requested: string
    upstream: string
    mapped: boolean
  }
  endpoint_type: string
  stream: boolean
  timing: {
    first_response_ms: number
    total_ms: number
  }
  usage: {
    prompt_tokens: number
    completion_tokens: number
    total_tokens: number
    cached_tokens: number
    cache_creation_tokens: number
    reasoning_tokens: number
    usage_source?: string
  }
  billing: {
    quota: number
    usd: number
  }
}

export type ChannelDebugResponse = {
  success: boolean
  message?: string
  error_code?: string
  data?: ChannelDebugResult
}
