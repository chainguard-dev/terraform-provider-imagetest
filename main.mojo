from memory import OwnedPointer


struct A:
    var a: OwnedPointer[Int]

    fn __init__(out self, a: OwnedPointer[Int]):
        print("init", a[])
        self.a = OwnedPointer(a[])

    fn __del__(owned self):
        print("del", self.a[])

    fn __moveinit__(out self, owned other: A):
        self.a = OwnedPointer(other.a[])
        print("moveinit", self.a[])

    fn __copyinit__(out self, other: A):
        self.a = OwnedPointer(other.a[] + 1)
        print("copyinit", self.a[])


fn main() raises:
    print("1")
    var a = A(OwnedPointer(1))
    print("2")
    var b = a
    print("3")
    a = b  # why does this trigger __copyinit__ twice?
    print("A:")
    print(a.a[])
    print("B:")
    print(b.a[])
