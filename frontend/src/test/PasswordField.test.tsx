import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { PasswordField } from '../components/PasswordField'

it('reveals and hides the entered password without submitting its form', async () => {
  const user = userEvent.setup()
  const submit = vi.fn()
  render(<form onSubmit={submit}><PasswordField label="Password" defaultValue="quiet-secret" /></form>)

  const input = screen.getByLabelText('Password')
  const toggle = screen.getByRole('button', { name: 'Show password' })
  expect(input).toHaveAttribute('type', 'password')
  expect(toggle).toHaveAttribute('type', 'button')
  await user.hover(toggle)
  expect(await screen.findByRole('tooltip')).toHaveTextContent('Show password')
  await user.unhover(toggle)

  await user.click(toggle)
  expect(input).toHaveAttribute('type', 'text')
  expect(input).toHaveValue('quiet-secret')

  const hideToggle = screen.getByRole('button', { name: 'Hide password' })
  await user.hover(hideToggle)
  expect(await screen.findByRole('tooltip')).toHaveTextContent('Hide password')
  await user.click(hideToggle)
  expect(input).toHaveAttribute('type', 'password')
  expect(submit).not.toHaveBeenCalled()
})
