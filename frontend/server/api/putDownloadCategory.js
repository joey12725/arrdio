export default eventHandler(async (event) => {
  const body = await readBody(event)


  const config = useRuntimeConfig()
  const apiURL = `${config.public.apiBase}/zlib/putcategory`

  try {
    const categories = await $fetch(apiURL, {
      method: 'PUT',
      body,
      headers: { 'Content-Type': 'application/json' }
    })
    return categories
  } catch (err) {
  console.error(err.data || err.message)
  throw createError({ statusCode: err.statusCode, message: err.data })
}
})
