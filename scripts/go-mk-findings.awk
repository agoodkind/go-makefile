function normalize_path(input_line,    output_line) {
    output_line = input_line
    if (pwd != "" && index(output_line, pwd) == 1) {
        output_line = substr(output_line, length(pwd) + 1)
    }
    if (cwd != "" && index(output_line, cwd) == 1) {
        output_line = substr(output_line, length(cwd) + 1)
    }
    while (index(output_line, "../") == 1) {
        output_line = substr(output_line, 4)
    }
    return output_line
}

function key_for(input_line,    output_line) {
    output_line = normalize_path(input_line)
    if (match(output_line, /:[0-9]+:[0-9]+:/)) {
        output_line = substr(output_line, 1, RSTART - 1) ":::" substr(output_line, RSTART + RLENGTH)
    }
    return output_line
}

function baseline_finding(input_line,    marker, finding) {
    if (input_line ~ /^[ \t]*$/ || input_line ~ /^#/) {
        return ""
    }
    marker = "\t# " label ":"
    finding = input_line
    if (index(input_line, marker) > 0) {
        finding = substr(input_line, 1, index(input_line, marker) - 1)
    }
    return normalize_path(finding)
}

function print_finding(input_line,    location, message) {
    input_line = normalize_path(input_line)
    if (match(input_line, /:[0-9]+:[0-9]+:/)) {
        location = substr(input_line, 1, RSTART + RLENGTH - 2)
        message = substr(input_line, RSTART + RLENGTH)
        sub(/^[ \t]+/, "", message)
        printf "  %s\n    %s\n", location, message
    } else {
        printf "  %s\n", input_line
    }
}

BEGIN {
    if (action == "") {
        action = "normalize"
    }
}

action == "map" && NR == FNR {
    keyset[$0] = 1
    next
}

action == "map" {
    if (key_for($0) in keyset) {
        print
    }
    next
}

action == "baseline" {
    finding = baseline_finding($0)
    if (finding != "") {
        print finding
    }
    next
}

action == "key" {
    print key_for($0)
    next
}

action == "print" {
    print_finding($0)
    next
}

{
    print normalize_path($0)
}
