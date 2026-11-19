import { cpSync, mkdirSync, readFileSync, writeFileSync } from 'node:fs'
import { createRequire } from 'node:module'
import { join, resolve } from 'node:path'
import { spawnSync } from 'node:child_process'
import { pathToFileURL } from 'node:url'
const root=process.env.LUXO_SQL_SCRATCH + '/ts'
const workspace=resolve(import.meta.dirname,'../../..')
function runtime(target) {
 mkdirSync(target,{recursive:true})
 const source=join(workspace,'packages/client')
 cpSync(join(source,'dist'),join(target,'dist'),{recursive:true})
 cpSync(join(source,'package.json'),join(target,'package.json'))
}
runtime(join(root,'node_modules/@luxojs/client'))
const response=await fetch(process.env.LUXO_SQL_SCHEMA_ENDPOINT+'?$schema=',{headers:{'X-Introspection-Key':'benchmark'}})
if(!response.ok) throw Error('schema status '+response.status)
const schema=await response.json()
writeFileSync(join(root,'schema.json'),JSON.stringify(schema))
if(process.env.LUXO_SQL_INFERRED==='1') {
 await generateInferred()
} else {
 const {generateTypes}=await import(join(root,'generator/codegen.mjs'))
 for(const format of ['cjs','esm']) {
 const dir=join(root,format)
 runtime(join(dir,'node_modules/@luxojs/client'))
 await generateTypes(schema,dir)
 writeFileSync(join(dir,'package.json'),JSON.stringify({type:format==='esm'?'module':'commonjs'}))
 writeFileSync(join(dir,'tsconfig.json'),JSON.stringify({compilerOptions:{target:'ES2022',module:'NodeNext',moduleResolution:'NodeNext',strict:true,outDir:'out',skipLibCheck:false},include:['*.ts']}))
 const p=spawnSync(join(workspace,'packages/vite-plugin/node_modules/.bin/tsc'),['-p',dir],{encoding:'utf8'})
 if(p.status!==0) throw Error(p.stdout+p.stderr)
 }
}
console.log('CJS/ESM clients generated and type checked')

async function generateInferred() {
 const plugin=join(workspace,'packages/vite-plugin')
 const require=createRequire(join(plugin,'package.json'))
 const {build}=await import(pathToFileURL(require.resolve('vite')).href)
 const source=process.env.LUXO_SQL_RELATIONS==='1'?'Relations.ts':'Inferred.ts'
 const original=readFileSync(join(import.meta.dirname,source),'utf8')
 for(const format of ['cjs','esm']) {
  const dir=join(root,format)
  const entry=join(dir,'src/main.ts')
  const {luxo}=format==='cjs'?require(join(plugin,'dist/index.js')):await import(join(plugin,'dist/index.mjs'))
  runtime(join(dir,'node_modules/@luxojs/client'))
  mkdirSync(join(dir,'src'),{recursive:true})
  writeFileSync(entry,original)
  writeFileSync(join(dir,'package.json'),JSON.stringify({type:format==='esm'?'module':'commonjs'}))
  writeFileSync(join(dir,'tsconfig.json'),JSON.stringify({compilerOptions:{target:'ES2022',module:'ESNext',moduleResolution:'Bundler',strict:true,noEmit:true,skipLibCheck:false,types:[]},include:['src/**/*.ts']}))
  await build({root:dir,configFile:false,logLevel:'warn',plugins:[
   luxo({schema:join(root,'schema.json'),outDir:'src/generated'}),
   {name:'capture-sql-inference',enforce:'pre',transform(code,id) {
    if(id===entry) writeFileSync(join(dir,'inferred-transformed.ts'),code)
   }},
  ],build:{target:'es2022',minify:false,outDir:'out',emptyOutDir:false,sourcemap:true,
   lib:{entry,formats:[format==='cjs'?'cjs':'es'],fileName:()=>`inferred.${format==='cjs'?'cjs':'mjs'}`},
   rollupOptions:{external:['@luxojs/client']}}})
  if(readFileSync(entry,'utf8')!==original) throw Error('inference modified original source')
  const checked=spawnSync(join(plugin,'node_modules/.bin/tsc'),['-p',dir],{encoding:'utf8'})
  if(checked.status!==0) throw Error(checked.stdout+checked.stderr)
 }
}
