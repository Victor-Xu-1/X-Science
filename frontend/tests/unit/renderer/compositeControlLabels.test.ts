import fs from 'node:fs';
import path from 'node:path';
import ts from 'typescript';
import { expect, it } from 'vitest';

const compositeControls = new Set(['Select', 'TreeSelect', 'Cascader', 'AutoComplete', 'DatePicker', 'TimePicker']);
const renderer = path.resolve('packages/desktop/src/renderer');

it('does not implicitly activate a composite picker through its internal native input', () => {
  const violations: string[] = [];
  for (const filename of sourceFiles(renderer)) {
    const source = ts.createSourceFile(
      filename,
      fs.readFileSync(filename, 'utf8'),
      ts.ScriptTarget.Latest,
      true,
      ts.ScriptKind.TSX
    );
    const controls = new Set<string>();
    for (const statement of source.statements) {
      if (
        !ts.isImportDeclaration(statement) ||
        !ts.isStringLiteral(statement.moduleSpecifier) ||
        statement.moduleSpecifier.text !== '@arco-design/web-react'
      )
        continue;
      const bindings = statement.importClause?.namedBindings;
      if (!bindings || !ts.isNamedImports(bindings)) continue;
      for (const element of bindings.elements) {
        if (compositeControls.has(element.propertyName?.text ?? element.name.text)) controls.add(element.name.text);
      }
    }
    visit(source, (node) => {
      if (!ts.isJsxElement(node) || node.openingElement.tagName.getText(source) !== 'label') return;
      if (
        node.openingElement.attributes.properties.some(
          (attribute) => ts.isJsxAttribute(attribute) && attribute.name.getText(source) === 'htmlFor'
        )
      )
        return;
      visit(node, (child) => {
        const tag =
          ts.isJsxOpeningElement(child) || ts.isJsxSelfClosingElement(child) ? child.tagName.getText(source) : '';
        if (!controls.has(tag)) return;
        const line = source.getLineAndCharacterOfPosition(child.getStart(source)).line + 1;
        violations.push(`${path.relative(process.cwd(), filename)}:${line} ${tag}`);
      });
    });
  }
  expect(violations, 'Composite pickers need an explicit accessible name, not implicit label forwarding.').toEqual([]);
});

function sourceFiles(directory: string): string[] {
  return fs.readdirSync(directory, { withFileTypes: true }).flatMap((entry) => {
    const filename = path.join(directory, entry.name);
    return entry.isDirectory() ? sourceFiles(filename) : filename.endsWith('.tsx') ? [filename] : [];
  });
}

function visit(node: ts.Node, action: (node: ts.Node) => void): void {
  action(node);
  node.forEachChild((child) => visit(child, action));
}
