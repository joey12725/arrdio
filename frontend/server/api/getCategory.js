export default eventHandler((event) => {
    
    const config = useRuntimeConfig()
    const apiURL = `${config.public.apiBase}/zlib/getcategory`

    // const apiURL = `http://127.0.0.1:8080/zlib/getcategory`

    const categories = $fetch(apiURL, {
        method: `GET`
    }) 
    
    return categories
})