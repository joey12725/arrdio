<script setup lang="ts">
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

    type FormattedCategory = {
        label: string
        value:string
    }

    defineProps<{
        books: BookRow[]
        categories: FormattedCategory[]
    }>()


    const carousel = useTemplateRef('carousel')
    const activeIndex = ref(0)

    function onClickPrev() {
        activeIndex.value--
    }

    function onClickNext() {
        activeIndex.value++
    }

    function onSelect(index: number) {
        activeIndex.value = index
    }

    function select(index: number) {
        activeIndex.value = index
        carousel.value?.emblaApi?.scrollTo(index)
    }
</script>

<template>
    <div class="pt-5 flex flex-col items-center"> 
        <h1 v-if="books[activeIndex].fetchStatus !== 'downloaded' || books[activeIndex].fetchStatus !== 'found'" class="mb-2 text-header text-[clamp(18px, 4vw, 35px)] font-header">{{ books[activeIndex].inputTitle }}</h1>
        <h1 v-else class="mb-2 text-header text-[clamp(18px, 4vw, 35px)] font-header">{{ books[activeIndex].retrievedTitle }}</h1>
        <UCarousel  ref="carousel" v-slot="{ item }" :items="books"  arrows :prev="{ color: 'primary', variant: 'solid', onClick: onClickPrev}" :next="{ color: 'primary', variant: 'solid', onClick: onClickNext}" @select="onSelect"  class="w-full max-w-xs mx-auto" >
            <!-- <div class=" flex flex-col items-center justify-start p-3 text-center"> -->
            <div v-if="!['found','downloaded'].includes(item?.fetchStatus)" class="placeholder-book rounded border border-gray-300 aspect-[2/3]">
                <NuxtImg v-if="item.bookType === 'audiobook'"  src="./audiobook_outline.png" arrows />
                <NuxtImg v-if="item.bookType === 'ebook'"  src="./book_outline.png" arrows />
            </div>
            <div v-else class="relative rounded border border-gray-300 aspect-[2/3] overflow-hidden ">
                <NuxtImg :src="item.imageLink" class="w-full h-full object-cover opacity-50" />
                <UButton
                    aria-label="Download"
                    color="neutral"
                    variant="ghost"
                    size="lg"
                    class="absolute inset-0 flex items-center justify-center"
                        @click="$emit('download', item)"
                    >

                    <UIcon name="material-symbols:download" size="40"/>
                </UButton>
            </div>
        </UCarousel>
            <div class="pt-1">
                <Badge :badgeStatus="books[activeIndex].fetchStatus"/>
            </div>
                 
      <div class="pt-3">  
        <USelectMenu
            :items="categories"
            class="w-40 "
            :modelValue= "books[activeIndex].downloadCategory"
            :onUpdate:modelValue= "(newVal: string) => {
            books[activeIndex].downloadCategory = newVal
            }"
            valueKey="value"/>
        </div>
    </div>   
</template>