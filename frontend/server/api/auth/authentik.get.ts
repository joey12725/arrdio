export default defineOAuthAuthentikEventHandler({
  async onSuccess(event, { user, tokens }) {
    await setUserSession(event, { user })
    return sendRedirect(event, '/')
  },
  onError(event, error) {
    console.error('Authentik OAuth error:', error)
    return sendRedirect(event, '/auth/login')
  }
})
