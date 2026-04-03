" Vim syntax file
" Language: Arca
" Filetype: *.arca

if exists("b:current_syntax")
  finish
endif

" Keywords
syntax keyword arcaKeyword fun type let match import pub assert defer for in static
syntax keyword arcaKeyword go tags

" Built-in types
syntax keyword arcaType Int Float String Bool List Option Result Unit

" Constants
syntax keyword arcaConstant True False None Unit

" Built-in constructors
syntax keyword arcaBuiltin Ok Error Some

" self / Self
syntax keyword arcaSelf self Self

" Operators
syntax match arcaOperator "|>"
syntax match arcaOperator "->"
syntax match arcaOperator "=>"
syntax match arcaOperator "\.\."
syntax match arcaOperator "?"

" Numbers
syntax match arcaNumber "\<\d\+\>"
syntax match arcaNumber "\<\d\+\.\d\+\>"

" Strings
syntax region arcaString start=/"/ skip=/\\"/ end=/"/ contains=arcaInterp
syntax match arcaInterp "\${[^}]*}" contained

" Comments
syntax match arcaComment "//.*$"

" Type names (PascalCase)
syntax match arcaTypeName "\<[A-Z][a-zA-Z0-9]*\>"

" Highlighting
highlight default link arcaKeyword Keyword
highlight default link arcaType Type
highlight default link arcaConstant Constant
highlight default link arcaBuiltin Special
highlight default link arcaSelf Special
highlight default link arcaOperator Operator
highlight default link arcaNumber Number
highlight default link arcaString String
highlight default link arcaInterp Special
highlight default link arcaComment Comment
highlight default link arcaTypeName Type

let b:current_syntax = "arca"
