import { useMemo } from 'react'
import CodeMirror, { EditorView } from '@uiw/react-codemirror'
import { css } from '@codemirror/lang-css'
import { html } from '@codemirror/lang-html'
import { javascript } from '@codemirror/lang-javascript'
import { json } from '@codemirror/lang-json'
import { markdown } from '@codemirror/lang-markdown'
import { StreamLanguage } from '@codemirror/language'
import { Prec } from '@codemirror/state'
import { go } from '@codemirror/legacy-modes/mode/go'
import { lua } from '@codemirror/legacy-modes/mode/lua'
import { properties } from '@codemirror/legacy-modes/mode/properties'
import { shell } from '@codemirror/legacy-modes/mode/shell'
import { toml } from '@codemirror/legacy-modes/mode/toml'
import { xml } from '@codemirror/legacy-modes/mode/xml'
import { yaml } from '@codemirror/legacy-modes/mode/yaml'
import { useTheme } from '@mui/material'
import { cspNonce } from '../emotion'

const legacyLanguages = {
  go: StreamLanguage.define(go),
  ini: StreamLanguage.define(properties),
  lua: StreamLanguage.define(lua),
  sh: StreamLanguage.define(shell),
  toml: StreamLanguage.define(toml),
  xml: StreamLanguage.define(xml),
  yaml: StreamLanguage.define(yaml),
}

const editorSecurity = cspNonce ? [EditorView.cspNonce.of(cspNonce)] : []

function languageForName(name: string) {
  const filename = name.toLowerCase()
  const extension = filename.split('.').pop() ?? ''
  if (extension === 'json') return json()
  if (['md', 'markdown'].includes(extension)) return markdown()
  if (extension === 'js') return javascript()
  if (extension === 'jsx') return javascript({ jsx: true })
  if (extension === 'ts') return javascript({ typescript: true })
  if (extension === 'tsx') return javascript({ jsx: true, typescript: true })
  if (['html', 'htm', 'xhtml'].includes(extension)) return html()
  if (extension === 'css') return css()
  if (['yaml', 'yml'].includes(extension)) return legacyLanguages.yaml
  if (['sh', 'bash', 'zsh'].includes(extension) || ['.bashrc', '.zshrc', '.profile'].includes(filename)) return legacyLanguages.sh
  if (extension === 'lua') return legacyLanguages.lua
  if (extension === 'go') return legacyLanguages.go
  if (extension === 'toml') return legacyLanguages.toml
  if (extension === 'ini') return legacyLanguages.ini
  if (extension === 'xml') return legacyLanguages.xml
  return null
}

export default function TextEditor({ name, value, onChange, readOnly = false }: { name: string; value: string; onChange?: (value: string) => void; readOnly?: boolean }) {
  const theme = useTheme()
  const language = useMemo(() => languageForName(name), [name])
  const surface = theme.palette.background.paper
  const surfaceTheme = useMemo(() => Prec.highest(EditorView.theme({
    '&.cm-editor': { backgroundColor: surface },
    '&.cm-editor .cm-gutters': { backgroundColor: surface },
  })), [surface])
  const extensions = useMemo(() => language ? [...editorSecurity, language, surfaceTheme] : [...editorSecurity, surfaceTheme], [language, surfaceTheme])
  return (
    <CodeMirror
      value={value}
      height={readOnly ? undefined : '100%'}
      minHeight={readOnly ? '240px' : undefined}
      maxHeight={readOnly ? '70vh' : undefined}
      theme={theme.palette.mode}
      extensions={extensions}
      editable={!readOnly}
      readOnly={readOnly}
      onChange={onChange}
      basicSetup={{
        lineNumbers: true,
        foldGutter: false,
        highlightActiveLine: !readOnly,
        highlightActiveLineGutter: !readOnly,
      }}
      style={{ height: readOnly ? undefined : '100%' }}
    />
  )
}
