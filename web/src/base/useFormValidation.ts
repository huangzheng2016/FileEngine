import { reactive, ref } from 'vue'

// A field shows its error after the first submit, or once the user has typed in it
// and left it. Merely passing through a field (autofocus, tabbing) shows nothing.
// Fields are identified by their name attribute; one listener object on the form
// covers them all.
//   const v = useFormValidation(() => ({ name: form.name.trim() ? '' : '请输入名称' }))
//   <form v-on="v.form" @submit.prevent="submit">
//     <input v-model="form.name" name="name" :aria-invalid="!!v.error('name')">
//     <span v-if="v.error('name')" class="field-error">{{ v.error('name') }}</span>
//   async function submit() { if (!v.validate()) return; ... }
export function useFormValidation<K extends string>(rules: () => Record<K, string>) {
  const submitted = ref(false)
  const edited = reactive(new Set<string>())
  const left = reactive(new Set<string>())

  const nameOf = (e: Event) => (e.target as HTMLInputElement | null)?.name ?? ''

  return {
    error: (key: K) => (submitted.value || (edited.has(key) && left.has(key)) ? rules()[key] : ''),
    form: {
      input: (e: Event) => void edited.add(nameOf(e)),
      focusout: (e: FocusEvent) => {
        if (edited.has(nameOf(e))) left.add(nameOf(e))
      },
    },
    validate: () => {
      submitted.value = true
      return Object.values<string>(rules()).every((e) => !e)
    },
    reset: () => {
      submitted.value = false
      edited.clear()
      left.clear()
    },
  }
}
