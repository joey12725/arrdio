export default defineNuxtRouteMiddleware((to) => {
  // Skip auth in development
  if (import.meta.dev) {
    return
  }

  const { loggedIn } = useUserSession()

  if (to.path.startsWith('/auth') || to.path.startsWith('/api/auth')) {
    return
  }

  if (!loggedIn.value) {
    return navigateTo('/auth/login')
  }
})
