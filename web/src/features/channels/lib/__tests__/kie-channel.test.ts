/*
Copyright (C) 2023-2026 QuantumNous

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as
published by the Free Software Foundation, either version 3 of the
License, or (at your option) any later version.
*/
import { describe, expect, test } from 'vitest'

import { CHANNEL_TYPE_KIE, CHANNEL_TYPE_OPTIONS } from '../../constants'
import { getChannelTypeConfig } from '../channel-type-config'
import { getChannelTypeIcon, getKeyPromptForType } from '../channel-utils'

describe('KIE AI channel', () => {
  test('registers its selection and official defaults', () => {
    expect(
      CHANNEL_TYPE_OPTIONS.find((item) => item.value === CHANNEL_TYPE_KIE)
    ).toEqual({ value: CHANNEL_TYPE_KIE, label: 'KIE AI' })
    expect(getChannelTypeIcon(CHANNEL_TYPE_KIE)).toBe('OpenAI')
    expect(getKeyPromptForType(CHANNEL_TYPE_KIE)).toBe('Enter KIE API key')
    expect(getChannelTypeConfig(CHANNEL_TYPE_KIE)).toMatchObject({
      defaultBaseUrl: 'https://api.kie.ai',
      supportedModels: ['gpt-5.5', 'gpt-image-2', 'grok-imagine-video'],
    })
  })
})
