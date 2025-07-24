import { readBody } from 'h3'

export default eventHandler(async (event) => {
  const body = await readBody(event)  
  
  const config = useRuntimeConfig()
  const apiURL = `${config.public.apiBase}/zlib/downloadbook`

  return await $fetch(apiURL, {
    method: 'POST',
    body,
    headers: { 'Content-Type': 'application/json' }
  })
})

// export default eventHandler(() => {
//   return { message: 'API is alive' }
// })
