import { readBody } from 'h3'

export default eventHandler(async (event) => {
  const body = await readBody(event)  

  const config = useRuntimeConfig()
  const prowlarrURL = `${config.public.apiBase}/prowlarr/searchbooks`
  const abbURL = `${config.public.apiBase}/abb/searchbooks`
  const openLibraryURL = `${config.public.apiBase}/openlibrary/search`

  var coverData = null
  var searchResults = null
  
  // Get cover images from OpenLibrary
  try {
    coverData = await $fetch(openLibraryURL, {
      method: 'POST',
      body,
      headers: { 'Content-Type': 'application/json' }
    })
  } catch (err) {
    console.log('OpenLibrary failed, continuing without covers:', err.message)
  }

  // Try Prowlarr first
  try {
    console.log('Trying MyAnonMouse...')
    searchResults = await $fetch(prowlarrURL, {
      method: 'POST',
      body,
      headers: { 'Content-Type': 'application/json' }
    })
    console.log('Prowlarr response:', JSON.stringify(searchResults, null, 2))
  } catch (err) {
    console.log('Prowlarr failed, trying AudiobookBay...')
    
    // Fallback to AudiobookBay
    try {
      searchResults = await $fetch(abbURL, {
        method: 'POST',
        body,
        headers: { 'Content-Type': 'application/json' }
      })
    } catch (abbErr) {
      console.error('Both services failed:', abbErr.message)
      throw createError({ 
        statusCode: 500, 
        statusMessage: 'All audiobook search services failed'
      })
    }
  }

  // Combine search results with cover data
  var results = []
  for (var i = 0; i < searchResults.results.length; i++) {
    var result = searchResults.results[i]
    var inputTitle = body.request[i]?.book?.title || result.title
    
    // Find matching cover data
    var cover = null
    if (coverData && coverData.results) {
      cover = coverData.results[i]
    }

    // If search failed for this book
    if (result.error) {
      console.log(`Book "${inputTitle}" error from Prowlarr:`, result.error)
      results.push({
        title: inputTitle,
        error: result.error
      })
      continue
    }

    // Build the final result
    var bookData = {
      title: inputTitle,
      book: {
        image_link: cover?.cover_url || result.book?.image_link || '',
        href_link: result.book?.href_link || '',
        magnet_link: result.magnet_link || result.book?.magnet_link,
        title: result.retrieved_title || result.book?.title || inputTitle,
        author: cover?.author || result.book?.author || ''
      }
    }

    results.push(bookData)
  }

  return { results }
})
