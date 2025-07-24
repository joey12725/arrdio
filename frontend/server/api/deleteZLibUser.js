export default eventHandler(async (event) => {
  const raw = await readRawBody(event, 'utf-8')
  // console.log('Raw payload:', raw)
  
  const config = useRuntimeConfig()
  const apiURL = `${config.public.apiBase}/zlib/deleteuser/${raw}.com`
  try {
    const users = await $fetch(apiURL, {
      method: 'DELETE',
    })
    return users
  } catch (err) {
  console.error(err.data || err.message)
  throw createError({ statusCode: err.statusCode, statusMessage: err.data || err.message })
}
})
