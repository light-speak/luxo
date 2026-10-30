import { cpSync, mkdirSync, writeFileSync } from 'node:fs'
import { join, resolve } from 'node:path'
import { spawnSync } from 'node:child_process'
const root=process.env.LUXO_SQL_SCRATCH + '/ts'
const workspace=resolve(import.meta.dirname,'../../..')
function runtime(target) {
 mkdirSync(target,{recursive:true})
 const source=join(workspace,'packages/client')
 cpSync(join(source,'dist'),join(target,'dist'),{recursive:true})
 cpSync(join(source,'package.json'),join(target,'package.json'))
}
runtime(join(root,'node_modules/@luxojs/client'))
const {generateTypes}=await import(join(root,'generator/codegen.mjs'))
const response=await fetch(process.env.LUXO_SQL_SCHEMA_ENDPOINT+'?$schema=',{headers:{'X-Introspection-Key':'benchmark'}})
if(!response.ok) throw Error('schema status '+response.status)
const schema=await response.json()
writeFileSync(join(root,'schema.json'),JSON.stringify(schema))
for(const format of ['cjs','esm']) {
 const dir=join(root,format)
 runtime(join(dir,'node_modules/@luxojs/client'))
 await generateTypes(schema,dir)
 writeFileSync(join(dir,'package.json'),JSON.stringify({type:format==='esm'?'module':'commonjs'}))
 writeFileSync(join(dir,'tsconfig.json'),JSON.stringify({compilerOptions:{target:'ES2022',module:'NodeNext',moduleResolution:'NodeNext',strict:true,outDir:'out',skipLibCheck:false},include:['*.ts']}))
 const p=spawnSync(join(workspace,'packages/vite-plugin/node_modules/.bin/tsc'),['-p',dir],{encoding:'utf8'})
 if(p.status!==0) throw Error(p.stdout+p.stderr)
}
console.log('CJS/ESM clients generated and type checked')
