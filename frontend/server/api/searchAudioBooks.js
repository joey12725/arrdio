import { readBody } from 'h3'

export default eventHandler(async (event) => {
  const body = await readBody(event)  


  const config = useRuntimeConfig()
  const apiURL = `${config.public.apiBase}/abb/searchbooks`

  try {
    const audiobooks = await $fetch(apiURL, {
      method: 'POST',
      body,
      headers: { 'Content-Type': 'application/json' }
    })
    return audiobooks
  } catch (err) {
    console.error(err.data || err.message)
    throw createError({ statusCode: err.statusCode, statusMessage: err.data.data || err.message })
  }
})
