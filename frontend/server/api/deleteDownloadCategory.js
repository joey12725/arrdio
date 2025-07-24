export default eventHandler(async (event) => {
  const raw = await readRawBody(event, 'utf-8')
  const config = useRuntimeConfig()
  const apiURL = `${config.public.apiBase}/zlib/deletecategory/${raw}.com`
  try {
    const category = await $fetch(apiURL, {
      method: 'DELETE',
    })
    return category
  } catch (err) {
  console.error(err.data || err.message)
  throw createError({ statusCode: err.statusCode, statusMessage: err.data || err.message })
}
})
