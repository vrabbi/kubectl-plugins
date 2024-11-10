git log --numstat --pretty=format:"%h %s | %cn | %cd" -- release.sh | awk '
/^[0-9]/ { added += $1; removed += $2; total_lines += $1 + $2 }
/^[a-f0-9]/ { commit_count++; print "Commit: " $0 }
END {
  print "Total Commits:", commit_count
  print "Total Lines Added:", added
  print "Total Lines Removed:", removed
  print "Total Lines Changed:", total_lines
}'
