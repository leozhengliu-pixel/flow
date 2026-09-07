import fs from 'node:fs'
import path from 'node:path'
import ts from 'typescript'

const sources = new Set(['@radix-ui/react-dialog', '@radix-ui/react-popover', '@radix-ui/react-dropdown-menu', '@radix-ui/react-context-menu', '@radix-ui/react-select', '@radix-ui/react-tooltip'])
const surfaces = new Set(['Content', 'SubContent', 'Overlay'])
const failures = []
let checked = 0
function scan(directory) {
  for (const entry of fs.readdirSync(directory, { withFileTypes: true })) {
    const file = path.join(directory, entry.name)
    if (entry.isDirectory()) { scan(file); continue }
    if (!file.endsWith('.tsx') || file.includes('.test.')) continue
    const ast = ts.createSourceFile(file, fs.readFileSync(file, 'utf8'), ts.ScriptTarget.Latest, true, ts.ScriptKind.TSX)
    const namespaces = new Set(), named = new Map()
    for (const statement of ast.statements) {
      if (!ts.isImportDeclaration(statement) || !sources.has(statement.moduleSpecifier.text)) continue
      const bindings = statement.importClause?.namedBindings
      if (!bindings) continue
      if (ts.isNamespaceImport(bindings)) namespaces.add(bindings.name.text)
      else for (const element of bindings.elements) named.set(element.name.text, (element.propertyName ?? element.name).text)
    }
    function visit(node) {
      if (ts.isJsxOpeningElement(node) || ts.isJsxSelfClosingElement(node)) {
        const tag = node.tagName.getText(ast), [namespace, component] = tag.split('.')
        if (surfaces.has(namespaces.has(namespace) ? component : named.get(tag))) {
          checked++
          if (!node.attributes.properties.some(attribute => ts.isJsxAttribute(attribute) && attribute.name.getText(ast) === 'data-flow-motion')) {
            failures.push(`${file}:${ast.getLineAndCharacterOfPosition(node.getStart()).line + 1} ${tag} is missing a motion profile`)
          }
        }
      }
      ts.forEachChild(node, visit)
    }
    visit(ast)
  }
}
scan(path.resolve('src'))
if (failures.length) { console.error(failures.join('\n')); process.exitCode = 1 }
else console.log(`Motion profiles: ${checked} Radix surfaces covered`)
