const maxCharacters = 1_000_000
const maxRows = 200
const maxColumns = 50
const maxCellCharacters = 4_096

function separatorFor(source: string) {
  const firstLine = source.split(/\r?\n/, 1)[0] ?? ''
  const commas = firstLine.split(',').length
  const semicolons = firstLine.split(';').length
  return semicolons > commas ? ';' : ','
}

export function parseCsv(source: string) {
  const input = source.slice(0, maxCharacters)
  const separator = separatorFor(input)
  const rows: string[][] = []
  let row: string[] = []
  let field = ''
  let quoted = false
  let truncated = source.length > input.length

  const pushField = () => {
    if (row.length < maxColumns) row.push(field.slice(0, maxCellCharacters))
    else truncated = true
    if (field.length > maxCellCharacters) truncated = true
    field = ''
  }
  const pushRow = () => {
    pushField()
    if (rows.length < maxRows) rows.push(row)
    else truncated = true
    row = []
  }

  for (let index = 0; index < input.length; index += 1) {
    const character = input[index]
    if (character === '"') {
      if (quoted && input[index + 1] === '"') {
        field += '"'
        index += 1
      } else {
        quoted = !quoted
      }
    } else if (character === separator && !quoted) {
      pushField()
    } else if ((character === '\n' || character === '\r') && !quoted) {
      if (character === '\r' && input[index + 1] === '\n') index += 1
      pushRow()
      if (rows.length >= maxRows) {
        truncated ||= index < input.length - 1
        break
      }
    } else {
      field += character
    }
  }
  if (field || row.length) pushRow()
  return { rows, truncated }
}
