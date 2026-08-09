import { useMemo } from 'react'
import CodeMirror from '@uiw/react-codemirror'
import { markdown } from '@codemirror/lang-markdown'
import { json } from '@codemirror/lang-json'
import { useTheme } from '@mui/material'

export default function TextEditor({ name, value, onChange }: { name: string; value: string; onChange: (value: string) => void }) {
  const theme = useTheme()
  const extension = name.split('.').pop()?.toLowerCase()
  const extensions = useMemo(() => extension === 'json' ? [json()] : ['md', 'markdown'].includes(extension ?? '') ? [markdown()] : [], [extension])
  return <CodeMirror value={value} height="100%" theme={theme.palette.mode} extensions={extensions} onChange={onChange} basicSetup={{ foldGutter: false }} style={{ height: '100%' }} />
}
