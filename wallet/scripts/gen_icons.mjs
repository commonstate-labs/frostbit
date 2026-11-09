import { readFileSync, writeFileSync, mkdirSync, existsSync } from 'node:fs'
import { Resvg } from '@resvg/resvg-js'

const svg = readFileSync('icons/icon.svg')
const sizes = [16, 32, 48, 128]

if (!existsSync('icons')) mkdirSync('icons', { recursive: true })

for (const size of sizes) {
  const resvg = new Resvg(svg, { fitTo: { mode: 'width', value: size } })
  const png = resvg.render().asPng()
  writeFileSync(`icons/icon-${size}.png`, png)
  console.log(`icons/icon-${size}.png (${png.length} bytes)`)
}
