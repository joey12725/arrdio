export default defineNuxtRouteMiddleware((to) => {
  const { loggedIn } = useUserSession()

  if (to.path.startsWith('/auth') || to.path.startsWith('/api/auth')) {
    return
  }

  if (!loggedIn.value) {
    return navigateTo('/auth/login')
  }
})
