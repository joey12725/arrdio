<script setup lang="ts">

import * as v from 'valibot';
import type { FormSubmitEvent, TableColumn } from '@nuxt/ui'
import { h, resolveComponent } from 'vue'
import { routeLocationKey, routerViewLocationKey } from 'vue-router';

const UBadge = resolveComponent('UBadge')
const UIcon = resolveComponent('UIcon')
const UButton = resolveComponent('UButton')
const toast = useToast()
const USelectMenu = resolveComponent('USelectMenu')

const BookSchema = v.object({
  title: v.pipe(v.string('Required'), v.trim(),  v.nonEmpty('Required')),
  downloadCategory: v.pipe(v.string(), v.trim()),
  bookType: v.pipe(v.string(), v.trim())
})


type Book = {inputTitle: String, downloadCategory: String, bookType: String, imageLink?: String, retrievedTitle?: String, downloadLink?: String}

type BookRow = {      // unique key
  inputTitle:  string
  downloadCategory: string
  bookType:    'audiobook' | 'ebook'
  // request/response lifecycle
  fetchStatus:      'pending' | 'loading' | 'found' | 'error' | 'downloaded' | 'notfound'
  // result fields (all optional until status === 'done')
  imageLink?:      string
  retrievedTitle?: string
  errorMessage?:   string
  magnetLink?: string
  id?: string
  hash?: string
  extension?: string
}

interface DownloadCategory {
        id: number
        category: string
        savepath: string
}
type Schema = v.InferOutput<typeof BookSchema>

const blankBook = (): Schema => ({ title: '', downloadCategory: '', bookType: 'both' })

const stateList = reactive<Book[]>([])

const books = ref<BookRow[]>([])

const isDownloading = ref(false)

const downloadCategories = ref<DownloadCategory[]>([])


const state = reactive<Schema>({
   title: '', downloadCategory: '' , bookType: 'both'
})

const hasDownloadableBooks = computed(() =>
  books.value.some(b => b.retrievedTitle)
)


const { data: fetchedCategories, error } = await useFetch<DownloadCategory[]>('/api/getCategory')
if (!error.value && fetchedCategories.value) {
  downloadCategories.value = fetchedCategories.value
  // console.log(downloadCategories.value)
} else {
  console.log(error.value)
}


const radioItems = ref([{value: 'audiobook', label: "Audio", icon: 'arcticons:audiobookshelf'}, {value: 'both', label: "Both", icon: 'bx:book-reader'}, {value: 'ebook', label: "EBook", icon: 'bx:book'}])

const columns: TableColumn<BookRow>[] = [
  {
    accessorKey: 'inputTitle',
    header: 'Input Title',
    cell: ({ row }) => `${row.getValue('inputTitle')}`
  }, 
  {
    accessorKey: 'downloadCategory',
    header: 'Download Category',
    cell: ({ row }) => {
      
      const formattedCategories = downloadCategories.value.map((category: DownloadCategory) => ({
        label: category.category,
        value: category.category
      }))
        
      return h(USelectMenu, {
        items: formattedCategories,
        class: "w-40",
        modelValue: row.original.downloadCategory,
        'onUpdate:modelValue': (newVal: string) => {
          row.original.downloadCategory = newVal
        },
        valueKey: 'value',
      })
    }
  },
  {
    accessorKey: 'fetchStatus',
    header: 'Status',
    cell: ({ row }) => {
      const status = row.getValue('fetchStatus') as BookRow['fetchStatus']

      const color = {
        found: 'success' as const,
        error: 'error' as const,
        pending: 'warning' as const,
        loading: 'info' as const,
        downloaded: 'success' as const,
        notfound: 'error' as const
      }[status]

      return h(UBadge, { class: 'capitalize', variant: 'subtle', color }, () =>
        status
      )
    }
  },
   {
    accessorKey: 'bookType',
    header: 'Book Type',
    cell: ({ row }) => {
      if (row.getValue('bookType') as string === "audiobook"){

      return h(UIcon, { name: "arcticons:audiobookshelf", size:28 }, () =>
        row.getValue('bookType')
      )}
      else{
        return h(UIcon, { name: "bx:book", size:28  }, () =>
        row.getValue('bookType')
      )
      }
    }
  },
  {
    accessorKey: 'retrievedTitle',
    header: 'Retrieved Title',
    cell: ({ row }) => { 
      if (hasDownloadableBooks.value){
        return `${row.getValue('retrievedTitle')}`
      }
      else{
        return null
      }
    }
  },

  {
    accessorKey: 'imageLink',
    header: 'Book Cover',
    cell: ({ row }) => {
      if (row.getValue('imageLink')){
        return h("img", {src: row.getValue('imageLink') as string, width: "12%", height: "12%"})
        
      }
      else{
        return null
      }
    }
      

  },
  {
    id: 'download',
     header: () => {
        if (hasDownloadableBooks.value) {
      return h(UButton, {
        icon: "material-symbols:download",
        color: "primary",
        variant: "solid",
        size: "md",
         onClick: () => {
          const downloadable = books.value.filter(b => b.retrievedTitle && b.fetchStatus === "found")
          downloadBook(downloadable)
        }
      }, () => 'Download All')
    }
    return null
  },
    cell: ({ row }) => {
      if (row.getValue('retrievedTitle')){
        if (row.getValue('fetchStatus') === "downloaded") { 
          return h(UIcon, {
            name: 'i-heroicons-check-circle',
            class: 'text-green-500 transition-opacity duration-500',
            size: 30
        })
      }
        return h(UButton, {icon: "meteor-icons:download", color: "neutral", variant: "outline", size:"xl", onClick: () => downloadBook([row.original])})
      }
      else{
        return
      }
    }
  }
]

function removeItem() {
  if (stateList) {
    books.value.pop()
  }
}

const tableLoading = computed(() =>
  books.value.some(r => r.fetchStatus === 'loading')
)

function onSubmit (e: FormSubmitEvent<Schema>) {
  const defaultCategoryFor = (type: 'audiobook' | 'ebook') =>
    type === 'audiobook' ? 'audiobooks' : 'ebooks'
  
  if (e.data.bookType === "both"){

    books.value.push({inputTitle: e.data.title, bookType: "audiobook", fetchStatus: "pending", downloadCategory: defaultCategoryFor("audiobook")} )    
    books.value.push({inputTitle: e.data.title, bookType: "ebook", fetchStatus: "pending", downloadCategory: defaultCategoryFor("ebook")}) 
  }
  
  else {
    books.value.push({inputTitle: e.data.title, bookType: e.data.bookType, fetchStatus: "pending", downloadCategory: defaultCategoryFor(e.data.bookType)}) 
  }
  Object.assign(state, blankBook())
}

async function onRequest () {

  books.value.forEach(row => {
    if (row.fetchStatus === 'pending') row.fetchStatus = 'loading'
  })                                                                                           

  const audiobooks = books.value.filter(book => book.bookType === "audiobook")
  
  const ebooks = books.value.filter(book => book.bookType === "ebook")
  
  const audiobookRequest = audiobooks.map(b => ({"book":{"title": b.inputTitle}}))

  // console.log(audiobookRequest)
  const ebookRequest = ebooks.map(b => ({"book":{"title": b.inputTitle}}))
  
  // console.log(audiobookRequest)
  
  
  const audiobookPromise = audiobookRequest.length ? (async () => {
        const { data: audiobookData, error: audiobookError } = await useFetch<
        {results: Array<{
          title: string, 
          book?:{
            image_link: string
            href_link: string
            magnet_link: string
            title: string
            author?: string
        }
          error?: string
        }>}>('/api/searchAudioBooks', {
          method: 'post',
          body: { request: audiobookRequest }
        })

        if (audiobookError.value) {


          toast.add({title: "Uh oh! Panikk ahhhh, there was an error!", 
            description: String(audiobookError.value?.statusMessage || audiobookError.value),
            icon: 'i-heroicons-exclamation-triangle',
            color: 'error'
          })
          
          books.value.forEach(row => {
            if (row.bookType === 'audiobook'){
            row.fetchStatus = 'error'
            row.errorMessage = audiobookError.value?.statusMessage}
          })
          
        }
        else{
        audiobookData.value!.results.forEach(res => {
          const row = books.value.find(r => r.inputTitle === res.title && r.bookType === 'audiobook' && r.fetchStatus === 'loading')
          if (!row) return
          if (res.error) {
            // console.log(`Error here: ${res.error}`)
            if (res.error === "error book not found"){
              // console.log("Yay")
              row.fetchStatus       = 'notfound'
              row.errorMessage = res.error
            } else{
            
            row.fetchStatus       = 'error'
            row.errorMessage = res.error
            }
          } else if (res.book) {
            row.fetchStatus         = 'found'
            row.imageLink      = res.book.image_link
            row.retrievedTitle = res.book.title
            row.magnetLink   = res.book.magnet_link
          }
        })
        }
      })() : Promise.resolve({ data: ref({ response: [] }), error: ref(null) })
    


  const ebookPromise = ebookRequest.length ?  (async () => {
    
    const { data: ebookData, error: ebookError } = await useFetch<
    {response: Array<{
      title: string, 
      book?:{
        cover: string
        hash: string
        id: string
        title: string
        author?: string
        extension?: string
    }
      error?: string
    }>}>('/api/searchEBook', {
      method: 'post',
      body: { request: ebookRequest }
    })

    if (ebookError.value) {


      toast.add({title: "Uh oh! Panik, there was an error!", 
        description: String(ebookError.value?.statusMessage || ebookError.value),
        icon: 'i-heroicons-exclamation-triangle',
        color: 'error'
      })

      books.value.forEach(row => {
        if (row.bookType === 'ebook'){
        row.fetchStatus = 'error'
        row.errorMessage = ebookError.value?.statusMessage}
      })
      
      
    }

    else{ 
     ebookData.value!.response.forEach(res => {

      const row = books.value.find(r => r.inputTitle === res.title && r.bookType === 'ebook' && r.fetchStatus === 'loading')
      if (!row) return
      if (res.error) {
        row.fetchStatus       = 'error'
        row.errorMessage = res.error
      } else if (res.book) {
        row.fetchStatus         = 'found'
        row.imageLink      = res.book.cover
        row.retrievedTitle = res.book.title
        row.id   = res.book.id
        row.hash   = res.book.hash
        row.extension = res.book.extension
      }
    })
  }  
  
})() : Promise.resolve({ data: ref({ response: [] }), error: ref(null) })

  // await Promise.allSettled([audiobookPromise, ebookPromise])
  
}



async function downloadBook(rows: Array<BookRow>) {
  const audiobooks = rows.filter(book => book.bookType === "audiobook")
  
  const ebooks = rows.filter(book => book.bookType === "ebook")
  
  const audiobookRequest = audiobooks.map(book => ({"magnet": book.magnetLink, "category": book.downloadCategory}))

  
  const ebookRequest = ebooks.map(b => ({"book":{"title": b.retrievedTitle, "author": "", "id": b.id, "hash": b.hash, "extension": b.extension}, "category": b.downloadCategory}))

  if (audiobookRequest.length > 0){
    // console.log(`Audiobook category is ${audiobookRequest[0].}`)
    const { data: audiobookData, error: audiobookError } = await useFetch<{
    response: Array<{
    magnet: string,
    error: string
    }>
    }>('/api/addTorrent', {
        method: 'post',
        body: { request: audiobookRequest }
     });

  if (audiobookError.value) {
    toast.add({title: "Uh oh! Panikk ahhhh, there was an error!", 
      description: String(audiobookError.value?.message || audiobookError.value),
      icon: 'i-heroicons-exclamation-triangle',
      color: 'error'
    })
    return
  }

  audiobookData.value!.response.forEach(res => {

    const row = rows.find(r => r.magnetLink == res.magnet && r.fetchStatus === 'found')
    if (!row) return
    if (res.error) {
      row.fetchStatus       = 'error'
      row.errorMessage = res.error
    } else {
      row.fetchStatus         = 'downloaded'
      
    //   const idx = rows.indexOf(row)
    //   if (idx !== -1) {
    //    setTimeout(() => {
    //    books.value.splice(idx, 1)
    //   }, 200)
    // }
    }
  })
}
  if (ebookRequest.length > 0){
    console.log(`Ebook category is ${ebookRequest[0].category}`)
  const { data: ebookData, error: ebookError } = await useFetch<{
    warning: string
    response: Array<{
      book?:{
        cover: string
        hash: string
        id: string
        title: string
        author?: string
    }
      error?: string
    }>
    }>('/api/downloadEbook', {
        method: 'post',
        body: { request: ebookRequest }
     });

  if (ebookError.value) {
    toast.add({title: "Uh oh! Panikk ahhhh, there was an error!", 
      description: String(ebookError.value?.message || ebookError.value),
      icon: 'i-heroicons-exclamation-triangle',
      color: 'error'
    })
    return
  }

  ebookData.value!.response.forEach(res => {

    const row = rows.find(r => r.retrievedTitle === res.book?.title && r.fetchStatus === "found")
    if (!row) return
    if (res.error) {
      // console.log("test")
      row.fetchStatus       = 'error'
      row.errorMessage = res.error
    } else {
      row.fetchStatus         = 'downloaded'

    //   const idx = rows.indexOf(row)
    //   if (idx !== -1) {
    //    setTimeout(() => {
    //     books.value.splice(idx, 1)
    //   }, 200)
    // }
    }
  })
  }

}


/*
TODO:
- Add row action for troubleshooting, showing warnings and errors in detail
- Add row action that links to the qbittorrent download or whatever ebook's equaivelent would be
- Have ebooks deal with warnings and partial content response codes  
*/
</script>

<template>
  <section class="flex items-center justify-center h-full w-full overflow-auto">
  
  <div class="flex flex-col gap-2 items-center justify-center">
      <UTable :data="books" v-if="books.length > 0" sticky class="pb-6 max-h-[400px] overflow-auto" :columns="columns" loading-color="primary" loading-animation="carousel" :loading="tableLoading"/>
    <div>

    <UForm  attach :schema="BookSchema" :state="state"  @submit="onSubmit">
    <div class="flex items-center justify-center">
      <UFormField label="Title" name="title">
      <UInput v-model="state.title"/>
    </UFormField>

  </div>
  


    <div class="flex items-center justify-center pt-4">
      <UButton color="primary" type="submit"  variant="subtle" size="sm" >
        Add Item
      </UButton>

      <UButton color="primary" variant="ghost" size="sm" @click="removeItem()">
        Remove Item
      </UButton>
      <UFormField name="bookType" >
      <URadioGroup v-model="state.bookType" orientation="horizontal" variant="table" default-value="Both" :items="radioItems" indicator="hidden" size="xs">

            <template #label="{ item }">
        <span class="flex justify-center py-2">
          <UTooltip :title="item.label">
            <UIcon :name="item.icon" class="size-7 text-primary"  />
          </UTooltip>
 
          <span class="sr-only">{{ item.label }}</span>
        </span>
      </template>
      </URadioGroup>
      </UFormField>
    </div>
    <div class="flex items-center justify-center">
    </div>
  </UForm>
  
</div>
  <div class=" pt-4">
    <UButton type="submit" size="lg" loading-auto  @click="onRequest" icon="i-lucide-search">
      Search
    </UButton>
  </div>
</div>
  </section>
</template>
 