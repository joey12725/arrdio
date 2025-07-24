export default eventHandler(async (event) => {
  const body = await readBody(event)
  // useful for debugging:
  // console.log('→ proxying to Go with body:', body)
  const config = useRuntimeConfig()
  const apiURL = `${config.public.apiBase}/zlib/putzlibuser`


  try {
    const users = await $fetch(apiURL, {
      method: 'PUT',
      body,
      headers: { 'Content-Type': 'application/json' }
    })
    return users
  } catch (err) {
  console.error(err.data || err.message)
  throw createError({ statusCode: err.statusCode, message: err.data })
}
})
