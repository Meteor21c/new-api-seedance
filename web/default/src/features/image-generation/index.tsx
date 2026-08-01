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

import { zodResolver } from '@hookform/resolvers/zod'
import { useMutation, useQuery } from '@tanstack/react-query'
import {
  Clock3,
  ExternalLink,
  ImageIcon,
  LoaderCircle,
  Sparkles,
} from 'lucide-react'
import { useCallback, useEffect, useRef, useState } from 'react'
import { useForm } from 'react-hook-form'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'
import { z } from 'zod'

import { Main } from '@/components/layout'
import { Alert, AlertDescription, AlertTitle } from '@/components/ui/alert'
import { Button } from '@/components/ui/button'
import {
  Card,
  CardContent,
  CardDescription,
  CardHeader,
  CardTitle,
} from '@/components/ui/card'
import {
  Form,
  FormControl,
  FormDescription,
  FormField,
  FormItem,
  FormLabel,
  FormMessage,
} from '@/components/ui/form'
import { Input } from '@/components/ui/input'
import { NativeSelect, NativeSelectOption } from '@/components/ui/native-select'
import { Textarea } from '@/components/ui/textarea'

import {
  GENERATION_HISTORY_LIMIT,
  GENERATION_PENDING_TTL_MS,
  readGenerationHistory,
  readGenerationRecord,
  removeGenerationRecord,
} from '../generation-storage'
import {
  createImageTracked,
  getImageModels,
  type GeneratedImage,
  type ImageHistoryEntry,
  type ImageGenerationRequest,
  type TrackedImageResult,
  type TrackedImageRequest,
} from './api'
import { loadImageGenerationHistory, revokeImageObjectUrls } from './storage'

const imageFormSchema = z.object({
  model: z.string().trim().min(1, 'Model is required'),
  prompt: z.string().trim().min(1, 'Prompt is required').max(32000),
  n: z.number().int().min(1).max(4),
  size: z.string(),
  quality: z.string(),
})

type ImageFormValues = z.infer<typeof imageFormSchema>

const imageRenderKeys = new WeakMap<GeneratedImage, string>()
let imageRenderKeySequence = 0

function getImageRenderKey(image: GeneratedImage, scope: string): string {
  let key = imageRenderKeys.get(image)
  if (!key) {
    imageRenderKeySequence += 1
    key = String(imageRenderKeySequence)
    imageRenderKeys.set(image, key)
  }
  return `${scope}:${key}`
}

function getImageSources(image: GeneratedImage): string[] {
  const sources: string[] = []
  if (image.url) sources.push(image.url)
  // Some OpenAI-compatible providers return both a short-lived URL and a
  // reusable Base64 payload. Keep the URL as the lightweight first choice,
  // but fall back to Base64 when the provider URL has expired or returns 404.
  if (image.b64_json) sources.push(`data:image/png;base64,${image.b64_json}`)
  return sources
}

type ImagePreviewProps = {
  image: GeneratedImage
  alt: string
  compact?: boolean
}

function ImagePreview({ image, alt, compact = false }: ImagePreviewProps) {
  const { t } = useTranslation()
  const sources = getImageSources(image)
  const [sourceIndex, setSourceIndex] = useState(0)
  const [failed, setFailed] = useState(false)

  useEffect(() => {
    setSourceIndex(0)
    setFailed(false)
  }, [image])

  const source = sources[sourceIndex] ?? ''
  const hasSource = sources.length > 0

  return (
    <div className='space-y-2'>
      {source && !failed ? (
        <img
          key={source}
          className={`bg-muted w-full rounded-md object-contain ${compact ? 'aspect-square' : 'max-h-[32rem]'}`}
          src={source}
          alt={alt}
          onError={() => {
            if (sourceIndex + 1 < sources.length) {
              setSourceIndex((index) => index + 1)
            } else {
              setFailed(true)
            }
          }}
        />
      ) : (
        <div className='bg-muted text-muted-foreground flex aspect-square items-center justify-center rounded-md p-4 text-center text-sm'>
          {hasSource
            ? t('Image URL expired or the upstream provider returned 404')
            : t('No image data returned')}
        </div>
      )}
      {source && !failed && (
        <Button
          className='w-full'
          size={compact ? 'sm' : undefined}
          variant='outline'
          render={<a href={source} target='_blank' rel='noreferrer' />}
        >
          <ExternalLink />
          {t('Open image')}
        </Button>
      )}
    </div>
  )
}

function imageRequestErrorMessage(error: unknown): string {
  const responseData = (
    error as {
      response?: {
        data?: {
          message?: string
          error?: { message?: string }
        }
      }
    }
  )?.response?.data
  return (
    responseData?.error?.message ||
    responseData?.message ||
    (error instanceof Error ? error.message : '')
  )
}

export function ImageGeneration() {
  const { t } = useTranslation()
  const storedObjectUrls = useRef<string[]>([])
  const componentMounted = useRef(true)
  const [images, setImages] = useState<GeneratedImage[]>(() => {
    return (
      readGenerationRecord<TrackedImageResult>('image-latest')?.value.images ??
      []
    )
  })
  const [imagesRestored, setImagesRestored] = useState(() =>
    Boolean(readGenerationRecord<TrackedImageResult>('image-latest'))
  )
  const [imageHistory, setImageHistory] = useState<ImageHistoryEntry[]>(() =>
    readGenerationHistory<ImageHistoryEntry>('image-history')
  )
  const [pendingAfterReload, setPendingAfterReload] = useState(() => {
    const pending = readGenerationRecord<TrackedImageRequest>(
      'image-pending',
      GENERATION_PENDING_TTL_MS
    )
    const result = readGenerationRecord<TrackedImageResult>('image-latest')
    return Boolean(pending && result?.value.id !== pending.value.id)
  })

  const refreshStoredImages = useCallback(async (restoreLatest: boolean) => {
    try {
      const stored = await loadImageGenerationHistory()
      if (!componentMounted.current) {
        revokeImageObjectUrls(stored.objectUrls)
        return
      }
      revokeImageObjectUrls(storedObjectUrls.current)
      storedObjectUrls.current = stored.objectUrls
      const fallbackEntries =
        readGenerationHistory<ImageHistoryEntry>('image-history')
      const storedIds = new Set(stored.entries.map((entry) => entry.id))
      const mergedEntries = [
        ...stored.entries,
        ...fallbackEntries.filter((entry) => !storedIds.has(entry.id)),
      ]
        .sort((left, right) => right.createdAt - left.createdAt)
        .slice(0, GENERATION_HISTORY_LIMIT)
      if (mergedEntries.length > 0) {
        setImageHistory(mergedEntries)
        if (restoreLatest) {
          setImages(mergedEntries[0].images)
          setImagesRestored(true)
        }
      }
    } catch {
      // IndexedDB can be unavailable in private browsing. URL-only legacy
      // localStorage records remain visible through the initial state.
    }
  }, [])

  useEffect(() => {
    componentMounted.current = true
    void refreshStoredImages(true)
    return () => {
      componentMounted.current = false
      revokeImageObjectUrls(storedObjectUrls.current)
      storedObjectUrls.current = []
    }
  }, [refreshStoredImages])

  const form = useForm<ImageFormValues>({
    resolver: zodResolver(imageFormSchema),
    defaultValues: {
      model: '',
      prompt: '',
      n: 1,
      size: 'auto',
      quality: 'auto',
    },
  })

  const modelsQuery = useQuery({
    queryKey: ['image-generation-models'],
    queryFn: getImageModels,
    retry: false,
  })
  let modelDescription = t(
    'No available image models are configured in Channels'
  )
  if (modelsQuery.isLoading) {
    modelDescription = t('Loading models from Channels')
  } else if (modelsQuery.data?.length) {
    modelDescription = t('{{count}} available image models', {
      count: modelsQuery.data.length,
    })
  }

  useEffect(() => {
    const firstModel = modelsQuery.data?.[0]
    if (!firstModel) return
    const currentModel = form.getValues('model')
    if (!modelsQuery.data?.includes(currentModel)) {
      form.setValue('model', firstModel)
    }
  }, [form, modelsQuery.data])

  const createMutation = useMutation({
    mutationFn: createImageTracked,
    onSuccess: (response) => {
      setImages(response.data ?? [])
      setImageHistory(readGenerationHistory<ImageHistoryEntry>('image-history'))
      void refreshStoredImages(false)
      setImagesRestored(false)
      setPendingAfterReload(false)
      removeGenerationRecord('image-pending')
      toast.success(t('Image generated successfully'))
    },
    onError: (error) => {
      setPendingAfterReload(false)
      removeGenerationRecord('image-pending')
      toast.error(
        imageRequestErrorMessage(error) || t('Image generation request failed')
      )
    },
  })

  // If the route was changed while the synchronous request was still running,
  // createImageTracked writes the result after it resolves. Pick that result
  // up when the user returns to this page.
  useEffect(() => {
    if (!pendingAfterReload) return

    const refreshStoredResult = () => {
      const result = readGenerationRecord<TrackedImageResult>('image-latest')
      const pending = readGenerationRecord<TrackedImageRequest>(
        'image-pending',
        GENERATION_PENDING_TTL_MS
      )

      if (result && (!pending || result.value.id === pending.value.id)) {
        void refreshStoredImages(true)
      }
      if (!pending || (result && result.value.id === pending.value.id)) {
        setPendingAfterReload(false)
      }
    }

    refreshStoredResult()
    const interval = window.setInterval(refreshStoredResult, 1000)
    return () => window.clearInterval(interval)
  }, [pendingAfterReload, refreshStoredImages])

  useEffect(() => {
    const isGenerating = createMutation.isPending || pendingAfterReload
    if (!isGenerating) return

    const handleBeforeUnload = (event: BeforeUnloadEvent) => {
      event.preventDefault()
      event.returnValue = ''
    }
    window.addEventListener('beforeunload', handleBeforeUnload)
    return () => window.removeEventListener('beforeunload', handleBeforeUnload)
  }, [createMutation.isPending, pendingAfterReload])

  const onSubmit = (values: ImageFormValues) => {
    if (createMutation.isPending || pendingAfterReload) {
      toast.error(
        t(
          'Wait for the current image request to finish before submitting another'
        )
      )
      return
    }
    const request: ImageGenerationRequest = {
      model: values.model.trim(),
      prompt: values.prompt.trim(),
      n: values.n,
      response_format: 'url',
    }
    if (values.size !== 'auto') request.size = values.size
    if (values.quality !== 'auto') request.quality = values.quality
    setPendingAfterReload(true)
    setImagesRestored(false)
    createMutation.mutate(request)
  }

  return (
    <Main className='space-y-6 overflow-x-hidden overflow-y-auto px-3 py-6 pb-12 sm:px-4'>
      <div>
        <h1 className='flex items-center gap-2 text-2xl font-semibold'>
          <ImageIcon className='size-6' />
          {t('Image Generation')}
        </h1>
        <p className='text-muted-foreground mt-1 text-sm'>
          {t('Generate images with configured image models')}
        </p>
      </div>

      <div className='grid gap-6 xl:grid-cols-[minmax(0,5fr)_minmax(360px,4fr)]'>
        <Card>
          <CardHeader>
            <CardTitle>{t('Generation settings')}</CardTitle>
            <CardDescription>
              {t('Images are generated by your configured upstream channels')}
            </CardDescription>
          </CardHeader>
          <CardContent>
            <Form {...form}>
              <form
                className='space-y-5'
                onSubmit={form.handleSubmit(onSubmit)}
              >
                <FormField
                  control={form.control}
                  name='model'
                  render={({ field }) => (
                    <FormItem>
                      <FormLabel>{t('Model')}</FormLabel>
                      <FormControl>
                        <NativeSelect
                          className='w-full'
                          disabled={
                            modelsQuery.isLoading ||
                            (modelsQuery.data?.length ?? 0) === 0
                          }
                          value={field.value}
                          onChange={field.onChange}
                        >
                          {(modelsQuery.data ?? []).map((model) => (
                            <NativeSelectOption key={model} value={model}>
                              {model}
                            </NativeSelectOption>
                          ))}
                        </NativeSelect>
                      </FormControl>
                      <FormDescription>{modelDescription}</FormDescription>
                      <FormMessage />
                    </FormItem>
                  )}
                />

                <FormField
                  control={form.control}
                  name='prompt'
                  render={({ field }) => (
                    <FormItem>
                      <FormLabel>{t('Prompt')}</FormLabel>
                      <FormControl>
                        <Textarea
                          {...field}
                          className='min-h-40 resize-y'
                          placeholder={t(
                            'Describe the image, composition, lighting, and style'
                          )}
                        />
                      </FormControl>
                      <FormMessage />
                    </FormItem>
                  )}
                />

                <div className='grid gap-4 md:grid-cols-3'>
                  <FormField
                    control={form.control}
                    name='size'
                    render={({ field }) => (
                      <FormItem>
                        <FormLabel>{t('Size')}</FormLabel>
                        <FormControl>
                          <NativeSelect
                            className='w-full'
                            value={field.value}
                            onChange={field.onChange}
                          >
                            {[
                              'auto',
                              '1024x1024',
                              '1024x1536',
                              '1536x1024',
                              '1024x1792',
                              '1792x1024',
                              '512x512',
                            ].map((size) => (
                              <NativeSelectOption key={size} value={size}>
                                {size === 'auto' ? t('Automatic') : size}
                              </NativeSelectOption>
                            ))}
                          </NativeSelect>
                        </FormControl>
                        <FormMessage />
                      </FormItem>
                    )}
                  />

                  <FormField
                    control={form.control}
                    name='quality'
                    render={({ field }) => (
                      <FormItem>
                        <FormLabel>{t('Quality')}</FormLabel>
                        <FormControl>
                          <NativeSelect
                            className='w-full'
                            value={field.value}
                            onChange={field.onChange}
                          >
                            {[
                              'auto',
                              'standard',
                              'hd',
                              'low',
                              'medium',
                              'high',
                            ].map((quality) => (
                              <NativeSelectOption key={quality} value={quality}>
                                {t(quality)}
                              </NativeSelectOption>
                            ))}
                          </NativeSelect>
                        </FormControl>
                        <FormMessage />
                      </FormItem>
                    )}
                  />

                  <FormField
                    control={form.control}
                    name='n'
                    render={({ field }) => (
                      <FormItem>
                        <FormLabel>{t('Image count')}</FormLabel>
                        <FormControl>
                          <Input
                            type='number'
                            min={1}
                            max={4}
                            value={field.value}
                            onChange={(event) =>
                              field.onChange(event.target.valueAsNumber)
                            }
                          />
                        </FormControl>
                        <FormMessage />
                      </FormItem>
                    )}
                  />
                </div>

                <Alert>
                  <Sparkles />
                  <AlertTitle>{t('Lightweight relay')}</AlertTitle>
                  <AlertDescription>
                    {t(
                      'Generated images are returned directly from the upstream and are not stored on this server'
                    )}
                  </AlertDescription>
                </Alert>

                {(createMutation.isPending || pendingAfterReload) && (
                  <Alert>
                    <Clock3 />
                    <AlertTitle>
                      {t('Image generation is still in progress')}
                    </AlertTitle>
                    <AlertDescription>
                      {t(
                        'Switching sections is supported, but refreshing or closing the browser may prevent a synchronous image result from being recovered'
                      )}
                    </AlertDescription>
                  </Alert>
                )}

                {!createMutation.isPending &&
                  !pendingAfterReload &&
                  imagesRestored && (
                    <Alert>
                      <ImageIcon />
                      <AlertDescription>
                        {t(
                          'This result was restored from this browser and will expire after 24 hours'
                        )}
                      </AlertDescription>
                    </Alert>
                  )}

                <Button
                  className='w-full'
                  type='submit'
                  disabled={
                    createMutation.isPending ||
                    pendingAfterReload ||
                    (modelsQuery.data?.length ?? 0) === 0
                  }
                >
                  {createMutation.isPending ? (
                    <LoaderCircle className='animate-spin' />
                  ) : (
                    <Sparkles />
                  )}
                  {t('Generate image')}
                </Button>
              </form>
            </Form>
          </CardContent>
        </Card>

        <div className='space-y-6'>
          <Card className='min-h-96'>
            <CardHeader>
              <CardTitle>{t('Generated images')}</CardTitle>
              <CardDescription>
                {t('Results from the latest request appear here')}
              </CardDescription>
            </CardHeader>
            <CardContent>
              {images.length === 0 ? (
                <div className='text-muted-foreground flex min-h-72 flex-col items-center justify-center gap-3 text-center'>
                  <ImageIcon className='size-12 opacity-40' />
                  <p>{t('Submit a request to see generated images here')}</p>
                </div>
              ) : (
                <div className='max-h-[70vh] space-y-4 overflow-y-auto pr-1'>
                  {images.map((image) => {
                    return (
                      <div
                        key={getImageRenderKey(image, 'latest')}
                        className='space-y-3 rounded-lg border p-3'
                      >
                        <ImagePreview
                          image={image}
                          alt={image.revised_prompt || t('Generated image')}
                        />
                        {image.revised_prompt && (
                          <p className='text-muted-foreground text-xs'>
                            {image.revised_prompt}
                          </p>
                        )}
                      </div>
                    )
                  })}
                </div>
              )}
            </CardContent>
          </Card>

          <Card>
            <CardHeader>
              <CardTitle>{t('Recent image generations')}</CardTitle>
              <CardDescription>
                {t(
                  'Recent image results are kept in this browser for up to 24 hours'
                )}
              </CardDescription>
            </CardHeader>
            <CardContent className='max-h-[38rem] overflow-y-auto'>
              {imageHistory.length === 0 ? (
                <p className='text-muted-foreground py-8 text-center text-sm'>
                  {t('No image generations yet')}
                </p>
              ) : (
                <div className='space-y-4'>
                  {imageHistory.map((entry) => {
                    const imagesWithSources = entry.images.filter(
                      (image) => getImageSources(image).length > 0
                    )
                    return (
                      <div
                        key={entry.id}
                        className='space-y-3 rounded-lg border p-3'
                      >
                        <div className='flex items-center justify-between gap-2'>
                          <span className='truncate text-sm font-medium'>
                            {entry.model}
                          </span>
                          <span className='text-muted-foreground shrink-0 text-xs'>
                            {new Date(entry.createdAt).toLocaleString()}
                          </span>
                        </div>
                        <p className='text-muted-foreground line-clamp-3 text-sm'>
                          {entry.prompt}
                        </p>
                        {imagesWithSources.length > 0 ? (
                          <div className='grid gap-2 sm:grid-cols-2'>
                            {imagesWithSources.map((image) => (
                              <div
                                key={getImageRenderKey(image, entry.id)}
                                className='space-y-2'
                              >
                                <ImagePreview
                                  image={image}
                                  alt={
                                    image.revised_prompt || t('Generated image')
                                  }
                                  compact
                                />
                              </div>
                            ))}
                          </div>
                        ) : (
                          <p className='text-muted-foreground bg-muted rounded-md p-3 text-xs'>
                            {t(
                              'The upstream did not return reusable image data for this result'
                            )}
                          </p>
                        )}
                        <Button
                          className='w-full'
                          size='sm'
                          variant='secondary'
                          disabled={imagesWithSources.length === 0}
                          onClick={() => {
                            setImages(entry.images)
                            setImagesRestored(true)
                          }}
                        >
                          {t('View')}
                        </Button>
                      </div>
                    )
                  })}
                </div>
              )}
            </CardContent>
          </Card>
        </div>
      </div>
    </Main>
  )
}
