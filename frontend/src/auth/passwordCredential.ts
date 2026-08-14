type PasswordCredentialConstructor = new (credentials: { id: string; password: string }) => Credential

export async function offerToSavePassword(password: string) {
  const PasswordCredential = (window as Window & { PasswordCredential?: PasswordCredentialConstructor }).PasswordCredential
  if (!PasswordCredential || !navigator.credentials?.store) return

  try {
    await navigator.credentials.store(new PasswordCredential({ id: 'owner', password }))
  } catch {
    // Saving credentials is optional and may be declined by the user or browser.
  }
}
