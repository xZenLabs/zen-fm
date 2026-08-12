type PasswordCredentialConstructor = new (credentials: { id: string; password: string }) => Credential

export async function offerToSavePassword(username: string, password: string) {
  const PasswordCredential = (window as Window & { PasswordCredential?: PasswordCredentialConstructor }).PasswordCredential
  if (!username || !PasswordCredential || !navigator.credentials?.store) return

  try {
    await navigator.credentials.store(new PasswordCredential({ id: username, password }))
  } catch {
    // Saving credentials is optional and may be declined by the user or browser.
  }
}
