import { SpringValue } from '@react-spring/web'
import { spring } from 'motion'

// Run after changing spring parameters and update the corresponding CSS variables.
for (const [name, tension] of [['enter', 1500], ['exit', 2000]]) {
  const value = new SpringValue(0)
  void value.start({ to: 1, config: { tension, friction: 100, precision: 0.01 } })
  const samples = [0]
  while (!value.idle && samples.length < 120) {
    value.advance(1000 / 60)
    samples.push(Number(value.get().toFixed(5)))
  }
  console.log(`--flow-popover-${name}: linear(${samples.join(',')}); /* ${Math.round((samples.length - 1) * 1000 / 60)}ms */`)
}
const surface = spring({ keyframes: [0, 1], duration: 300, bounce: 0 })
console.log(`--flow-surface-ease: linear(${Array.from({ length: 21 }, (_, index) => Number(surface.next(index * 15).value.toFixed(5))).join(',')});`)
