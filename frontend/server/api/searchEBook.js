// server/api/searchEBook.ts
import { readBody, createError } from 'h3'
import { FetchError } from 'ofetch'

export default eventHandler(async (event) => {

  const body = await readBody(event)

  const config = useRuntimeConfig()
  const apiURL = `${config.public.apiBase}/zlib/searchbook`


  try {
    return await $fetch(apiURL, {
      method: 'POST',
      body,
      headers: { 'Content-Type': 'application/json' }
    })
  } catch (err) {
    console.error(err.data || err.message)
    throw createError({ statusCode: err.statusCode, statusMessage: err.data.data || err.message })
  }
})
