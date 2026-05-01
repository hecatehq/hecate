# Third-Party Notices

Hecate uses third-party data released under permissive licenses.
Copyright notices and license texts are included below.

## LiteLLM model pricing data

Hecate's pricebook import feature
(`/admin/control-plane/pricebook/import/preview` and `.../apply`,
implemented in `internal/billing/litellm/`) fetches the
`model_prices_and_context_window.json` file maintained by the LiteLLM
project at <https://github.com/BerriAI/litellm>. The file is fetched at
runtime when an operator triggers an import; Hecate does not vendor a
copy in the repository or in the published binaries.

LiteLLM is released under the MIT License. The LiteLLM copyright and
license notice is reproduced here in accordance with the license terms:

```
MIT License

Copyright (c) 2023 Berri AI

Permission is hereby granted, free of charge, to any person obtaining a copy
of this software and associated documentation files (the "Software"), to deal
in the Software without restriction, including without limitation the rights
to use, copy, modify, merge, publish, distribute, sublicense, and/or sell
copies of the Software, and to permit persons to whom the Software is
furnished to do so, subject to the following conditions:

The above copyright notice and this permission notice shall be included in all
copies or substantial portions of the Software.

THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR
IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY,
FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE
AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER
LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM,
OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN THE
SOFTWARE.
```

The upstream license file lives at
<https://github.com/BerriAI/litellm/blob/main/LICENSE>.
