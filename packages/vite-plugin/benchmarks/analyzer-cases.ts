import type { LuxoSchema } from '@luxojs/client'

export const schema: LuxoSchema = {
  models: { User: { name: 'User', fields: [
    { id: 1, name: 'id', type: 'Int' }, { id: 2, name: 'name', type: 'String' },
    { id: 3, name: 'score', type: 'Float' }, { id: 4, name: 'email', type: 'String' },
    { id: 5, name: 'manager', type: 'Model', typeName: 'User', relation: true },
  ] } },
  apis: {
    getUser: { id: 1, name: 'getUser', module: 'users', returnType: 'User' },
    allUsers: { id: 2, name: 'allUsers', module: 'users', returnType: 'User', returnList: true },
  },
}

function repeatedCalls(count: number): string {
  return Array.from({ length: count }, (_, index) =>
    `const u${index} = await client.getUser(); consume(u${index}.name)`
  ).join('\n')
}

export const equivalentCases: Readonly<Record<string, string>> = {
  noSDK: 'const row = { name: "local" }; consume(row.name)',
  scalar: 'const user = await client.getUser(); consume(user.name)',
  relation: 'const user = await client.getUser(); consume(user.manager.name)',
  map: 'const users = await client.allUsers(); consume(users.map(user => user.name))',
  filterMap: 'const users = await client.allUsers(); consume(users.filter(user => user.score > 0).map(user => user.name))',
  filterAlias: 'const users = await client.allUsers(); const active = users.filter(user => user.score > 0); consume(active.map(user => user.name))',
  optional: 'const users = await client.allUsers(); consume(users?.filter(user => user.score > 0).map(user => user.name))',
  explicit: 'const user = await client.getUser({ $select: "name" }); consume(user.name)',
  opaque: 'const user = await client.getUser(); consume(user)',
  calls32: repeatedCalls(32),
  calls128: repeatedCalls(128),
}

// These inputs change projections or fix a prior overflow. Keep their timings
// separate from equivalent comparisons; a shorter invalid output is not a gain.
export const correctedCases: Readonly<Record<string, string>> = {
  container: 'const users = await client.allUsers(); const packed = { first: users[0]! }; consume(packed.first.name)',
  rest: 'const users = await client.allUsers(); const [,...rest] = [0, users[0]!]; consume(rest[0].name)',
  defaults: 'const one = await client.getUser(); const two = await client.getUser(); const { first: chosen = two } = { first: one }; consume(chosen.name)',
  callbackRest: 'const users = await client.allUsers(); consume(users.map((...args) => args[0].name))',
  nativeLiteral: 'const user = await client.getUser(); consume([user].map(item => item.name))',
  opaqueSort: 'const users = await client.allUsers(); consume(users.sort((left, right) => left.name.localeCompare(right.name)))',
  aliases4096: 'const users = await client.allUsers(); const a0 = { first: users[0]! }; ' +
    Array.from({ length: 4096 }, (_, index) => `const a${index + 1} = a${index}`).join('\n') +
    '; consume(a4096.first.name)',
}

export const errorCases: Readonly<Record<string, string>> = { syntaxError: 'const =' }
