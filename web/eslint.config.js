import pluginVue from 'eslint-plugin-vue'
import tseslint from 'typescript-eslint'
import prettier from 'eslint-config-prettier'

export default tseslint.config(
  { ignores: ['dist/**', 'coverage/**'] },
  ...tseslint.configs.recommended,
  ...pluginVue.configs['flat/recommended'],
  { files: ['**/*.vue'], languageOptions: { parserOptions: { parser: tseslint.parser } } },
  // components are always imported explicitly, never registered globally, so single-word names cannot clash
  { rules: { 'vue/multi-word-component-names': 'off' } },
  // tests mount small inline harness components next to each other
  { files: ['src/test/**'], rules: { 'vue/one-component-per-file': 'off' } },
  prettier,
)
