package notice

import (
	"go/token"
	"reflect"
	"strconv"
	"strings"
)

var positionType = reflect.TypeOf(token.NoPos)

var excludedSyntaxFields = map[string]bool{
	"Obj":        true,
	"Scope":      true,
	"Unresolved": true,
	"Doc":        true,
	"Comment":    true,
	"Comments":   true,
}

type syntaxPosition struct {
	position token.Pos
	path     string
}

type syntaxEncoder struct {
	contents  strings.Builder
	positions []syntaxPosition
}

func (encoder *syntaxEncoder) value(value reflect.Value, path string) error {
	if !value.IsValid() {
		encoder.contents.WriteString("nil;")

		return nil
	}

	if value.Type() == positionType {
		encoder.contents.WriteString("position:" + strconv.FormatBool(value.Int() != 0) + ";")
		if value.Int() != 0 && !strings.HasSuffix(path, ".FileStart") &&
			!strings.HasSuffix(path, ".FileEnd") {
			encoder.positions = append(
				encoder.positions,
				syntaxPosition{position: token.Pos(value.Int()), path: path},
			)
		}

		return nil
	}

	switch value.Kind() {
	case reflect.Interface, reflect.Pointer:
		if value.IsNil() {
			encoder.contents.WriteString("nil;")

			return nil
		}

		return encoder.value(value.Elem(), path)
	case reflect.Struct:
		return encoder.structure(value, path)
	case reflect.Slice:
		return encoder.slice(value, path)
	case reflect.String:
		encoder.contents.WriteString("string:" + strconv.Quote(value.String()) + ";")
	case reflect.Bool:
		encoder.contents.WriteString("bool:" + strconv.FormatBool(value.Bool()) + ";")
	case reflect.Int:
		encoder.contents.WriteString("int:" + strconv.FormatInt(value.Int(), 10) + ";")
	case reflect.Invalid, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
		reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64,
		reflect.Uintptr, reflect.Float32, reflect.Float64, reflect.Complex64,
		reflect.Complex128, reflect.Array, reflect.Chan, reflect.Func, reflect.Map,
		reflect.UnsafePointer:
		return &syntaxFieldError{field: value.Type().String()}
	}

	return nil
}

func (encoder *syntaxEncoder) structure(value reflect.Value, path string) error {
	encoder.contents.WriteString(value.Type().String() + "{")

	for index := range value.NumField() {
		name := value.Type().Field(index).Name
		if excludedSyntaxFields[name] {
			continue
		}

		encoder.contents.WriteString(strconv.Quote(name) + ":")
		if err := encoder.value(value.Field(index), path+"."+name); err != nil {
			return err
		}
	}

	encoder.contents.WriteString("};")

	return nil
}

func (encoder *syntaxEncoder) slice(value reflect.Value, path string) error {
	encoder.contents.WriteString("[")

	for index := range value.Len() {
		if err := encoder.value(value.Index(index), path+"["+strconv.Itoa(index)+"]"); err != nil {
			return err
		}
	}

	encoder.contents.WriteString("];")

	return nil
}
