/*
Copyright (C) 2025 QuantumNous

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as published by
the Free Software Foundation, either version 3 of the License, or
(at your option) any later version.

This program is distributed in the hope that it will be useful,
but WITHOUT ANY WARRANTY; without even the implied warranty of
MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE. See the
GNU Affero General Public License for more details.

You should have received a copy of the GNU Affero General Public License
along with this program. If not, see <https://www.gnu.org/licenses/>.

For commercial licensing, please contact support@quantumnous.com
*/

import { useTranslation } from 'react-i18next'

import { Badge } from '@/components/ui/badge'

import type {
  VideoBillingMode,
  VideoModelKind,
  VideoResolution,
  VideoTier,
} from './types'

type ResolutionPriceTable = Partial<Record<VideoResolution, number>>

// Official reference prices read from the FZYinghe model marketplace on
// 2026-08-14. The marketplace lists cheap Seedance at 60% and Kling V3 at
// 65% of these official prices; the values below intentionally do not use the
// upstream discounted cost.
const OFFICIAL_SEEDANCE_PER_SECOND: Record<VideoTier, ResolutionPriceTable> = {
  standard: { '480p': 0.6, '720p': 1.2, '1080p': 3, '4K': 6 },
  fast: { '480p': 0.48, '720p': 0.96 },
  mini: { '480p': 0.3, '720p': 0.6 },
}

const OFFICIAL_KLING_V3 = {
  silent: { '720p': 0.72, '1080p': 0.96, '4K': 3.6 },
  audio: { '720p': 1.32, '1080p': 1.68, '4K': 3.6 },
} satisfies Record<string, ResolutionPriceTable>

const OFFICIAL_KLING_V3_OMNI = {
  noReferenceVideo: {
    silent: { '720p': 0.72, '1080p': 0.96, '4K': 3.6 },
    audio: { '720p': 0.96, '1080p': 1.2, '4K': 3.6 },
  },
  referenceVideo: {
    silent: { '720p': 1.08, '1080p': 1.44, '4K': 3.6 },
    audio: { '720p': 1.32, '1080p': 1.68, '4K': 3.6 },
  },
} satisfies Record<string, Record<string, ResolutionPriceTable>>

const OFFICIAL_TOKEN_PRICES: Record<
  string,
  {
    withoutInputVideo: ResolutionPriceTable
    withInputVideo: ResolutionPriceTable
  }
> = {
  'doubao-seedance-2.0': {
    withoutInputVideo: { '480p': 46, '720p': 46, '1080p': 51, '4K': 26 },
    withInputVideo: { '480p': 28, '720p': 28, '1080p': 31, '4K': 16 },
  },
  'doubao-seedance-2.0-fast': {
    withoutInputVideo: { '480p': 37, '720p': 37 },
    withInputVideo: { '480p': 22, '720p': 22 },
  },
  'doubao-seedance-2.0-mini': {
    withoutInputVideo: { '480p': 23, '720p': 23 },
    withInputVideo: { '480p': 14, '720p': 14 },
  },
  'doubao-seedance-2.5': {
    withoutInputVideo: { '480p': 70, '720p': 70 },
    withInputVideo: { '480p': 42, '720p': 42 },
  },
}

function officialTokenPrice(
  pricingReference: string,
  resolution: VideoResolution,
  hasInputVideo: boolean
): number | undefined {
  const table = OFFICIAL_TOKEN_PRICES[pricingReference.toLowerCase()]
  if (!table) return undefined
  return (hasInputVideo ? table.withInputVideo : table.withoutInputVideo)[
    resolution
  ]
}

function officialTokenBasePrice(pricingReference: string): number | undefined {
  const table = OFFICIAL_TOKEN_PRICES[pricingReference.toLowerCase()]
  if (!table) return undefined
  return table.withoutInputVideo['720p']
}

function tokenPriceForScenario(
  configuredBasePrice: number | undefined,
  pricingReference: string,
  resolution: VideoResolution,
  hasInputVideo: boolean
): number | undefined {
  if (!Number.isFinite(configuredBasePrice)) return undefined
  const officialBase = officialTokenBasePrice(pricingReference)
  const officialScenario = officialTokenPrice(
    pricingReference,
    resolution,
    hasInputVideo
  )
  if (!officialBase || !officialScenario) return configuredBasePrice
  return (configuredBasePrice ?? 0) * (officialScenario / officialBase)
}

function officialPerSecondPrice(
  pricingReference: string,
  kind: VideoModelKind,
  tier: VideoTier,
  resolution: VideoResolution,
  audio: boolean,
  hasInputVideo: boolean
): number | undefined {
  const normalized = pricingReference.toLowerCase()
  if (normalized.startsWith('cheap-seedance-2.0')) {
    return OFFICIAL_SEEDANCE_PER_SECOND[tier][resolution]
  }
  if (normalized === 'kling-v3' || kind === 'kling-v3') {
    const table: ResolutionPriceTable = audio
      ? OFFICIAL_KLING_V3.audio
      : OFFICIAL_KLING_V3.silent
    return table[resolution]
  }
  if (normalized === 'kling-v3-omni' || kind === 'kling-v3-omni') {
    const table = hasInputVideo
      ? OFFICIAL_KLING_V3_OMNI.referenceVideo
      : OFFICIAL_KLING_V3_OMNI.noReferenceVideo
    const scenarioTable: ResolutionPriceTable = audio
      ? table.audio
      : table.silent
    return scenarioTable[resolution]
  }
  return undefined
}

function formatPrice(value: number | undefined): string {
  if (!Number.isFinite(value)) return '--'
  return `¥${(value ?? 0).toFixed(4)}`
}

interface VideoPriceComparisonProps {
  billingMode: VideoBillingMode
  pricingReference: string
  kind: VideoModelKind
  tier: VideoTier
  resolution: VideoResolution
  audio: boolean
  hasInputVideo: boolean
  duration: number
  configuredTokenBasePrice?: number
  currentPerSecondPrice?: number
}

export function VideoPriceComparison({
  billingMode,
  pricingReference,
  kind,
  tier,
  resolution,
  audio,
  hasInputVideo,
  duration,
  configuredTokenBasePrice,
  currentPerSecondPrice,
}: VideoPriceComparisonProps) {
  const { t } = useTranslation()
  const isTokenBilling = billingMode === 'per-token'
  const officialPrice = isTokenBilling
    ? officialTokenPrice(pricingReference, resolution, hasInputVideo)
    : officialPerSecondPrice(
        pricingReference,
        kind,
        tier,
        resolution,
        audio,
        hasInputVideo
      )
  const currentPrice = isTokenBilling
    ? tokenPriceForScenario(
        configuredTokenBasePrice,
        pricingReference,
        resolution,
        hasInputVideo
      )
    : currentPerSecondPrice
  const ratio =
    Number.isFinite(currentPrice) &&
    Number.isFinite(officialPrice) &&
    (officialPrice ?? 0) > 0
      ? (currentPrice ?? 0) / (officialPrice ?? 1)
      : undefined
  const unit = isTokenBilling ? ' / 1M Tokens' : ` / ${t('second')}`
  const estimatedTotal =
    !isTokenBilling &&
    Number.isFinite(currentPrice) &&
    Number.isFinite(duration)
      ? (currentPrice ?? 0) * duration
      : undefined

  return (
    <div className='bg-muted/50 space-y-4 rounded-lg border p-4'>
      <div className='flex flex-wrap items-start justify-between gap-3'>
        <p className='text-sm font-semibold'>{t('Price comparison')}</p>
        <Badge variant='outline'>
          {isTokenBilling ? t('Token billing') : t('Per-second billing')}
        </Badge>
      </div>

      <div className='grid gap-3 sm:grid-cols-3'>
        <div className='bg-background rounded-md border p-3'>
          <p className='text-muted-foreground text-xs'>{t('Site price')}</p>
          <p className='mt-1 text-lg font-semibold tabular-nums'>
            {formatPrice(currentPrice)}
            <span className='text-muted-foreground text-xs font-normal'>
              {unit}
            </span>
          </p>
        </div>
        <div className='bg-background rounded-md border p-3'>
          <p className='text-muted-foreground text-xs'>
            {t('Official reference price')}
          </p>
          <p className='mt-1 text-lg font-semibold tabular-nums'>
            {formatPrice(officialPrice)}
            {Number.isFinite(officialPrice) && (
              <span className='text-muted-foreground text-xs font-normal'>
                {unit}
              </span>
            )}
          </p>
        </div>
        <div className='bg-background rounded-md border p-3'>
          <p className='text-muted-foreground text-xs'>{t('Discount ratio')}</p>
          {Number.isFinite(ratio) ? (
            <>
              <p
                className={
                  (ratio ?? 0) <= 1
                    ? 'mt-1 text-lg font-semibold text-emerald-600 tabular-nums dark:text-emerald-400'
                    : 'mt-1 text-lg font-semibold text-amber-600 tabular-nums dark:text-amber-400'
                }
              >
                {t('Equivalent to {{discount}}/10 of official price', {
                  discount: ((ratio ?? 0) * 10).toFixed(1),
                })}
              </p>
              <p className='text-muted-foreground text-xs tabular-nums'>
                {t('{{percent}}% of official price', {
                  percent: ((ratio ?? 0) * 100).toFixed(1),
                })}
              </p>
            </>
          ) : (
            <p className='text-muted-foreground mt-1 text-sm'>
              {t('Official reference unavailable')}
            </p>
          )}
        </div>
      </div>

      <div className='text-muted-foreground flex flex-wrap justify-end gap-2 text-xs'>
        {isTokenBilling ? (
          <span>{t('Actual returned usage prevails')}</span>
        ) : (
          <span className='text-foreground font-medium tabular-nums'>
            {t('Estimated {{duration}}-second total', { duration })}:{' '}
            {formatPrice(estimatedTotal)}
          </span>
        )}
      </div>
    </div>
  )
}
