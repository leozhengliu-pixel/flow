import { describe, expect, it } from 'vitest'

import { descriptionImageSrcs } from './image-extension'

describe('descriptionImageSrcs', () => {
  it('collects Markdown and document image sources', () => {
    const srcs = descriptionImageSrcs('See ![one](/uploads/a.png) and later', JSON.stringify({
      type: 'doc',
      content: [{ type: 'image', attrs: { src: '/uploads/b.png', alt: 'two' } }],
    }), { type: 'doc', content: [{ type: 'image', attrs: { src: '/uploads/c.png' } }] })
    expect([...srcs]).toEqual(expect.arrayContaining(['/uploads/a.png', '/uploads/b.png', '/uploads/c.png']))
  })
})
