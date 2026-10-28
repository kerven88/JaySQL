import { parse, compileScript, compileTemplate } from '@vue/compiler-sfc'
import { readFileSync } from 'node:fs'
const f = 'src/views/admin/system/ai/index.vue'
const { descriptor, errors } = parse(readFileSync(f, 'utf8'), { filename: f })
if (errors.length) { console.log('FAIL', errors[0].message); process.exit(1) }
compileScript(descriptor, { id: 'x' })
const r = compileTemplate({ source: descriptor.template.content, filename: f, id: 'x' })
if (r.errors.length) { console.log('FAIL', r.errors[0].message); process.exit(1) }
console.log('SFC OK')
