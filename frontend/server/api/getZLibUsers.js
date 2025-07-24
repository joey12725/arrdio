
export default eventHandler((event) => {
    

    const config = useRuntimeConfig()
    const apiURL = `${config.public.apiBase}/zlib/getzlibuser`


    // const apiURL = `http://127.0.0.1:8080/zlib/getzlibuser`

    const users = $fetch(apiURL, {
        method: `GET`
    }) 
    
    return users
})