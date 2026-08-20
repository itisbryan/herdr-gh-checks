" Loaded into the CI plugin's review diff. `ga` on a line appends
"   path:line  note
" to $CI_NOTES (the PR review file), so the agent gets exact line anchors.
function! s:Annotate() abort
  let l:notes = $CI_NOTES
  if empty(l:notes) | echo 'no CI_NOTES' | return | endif
  let l:path = empty($CI_REVIEW_PATH) ? expand('%:.') : $CI_REVIEW_PATH
  let l:where = l:path . ':' . line('.')
  let l:note = input('note ' . l:where . '  ')
  if empty(l:note) | return | endif
  call writefile([l:where . '  ' . l:note], l:notes, 'a')
  echo 'noted ' . l:where
endfunction
nnoremap <silent> ga :call <SID>Annotate()<CR>

" Persistent reminder so you don't forget the keys.
if exists('+winbar')
  let &winbar = '%#Comment# ga → annotate this line   ·   :qa → done reviewing'
endif
echo 'review: ga to annotate a line · :qa when done'
