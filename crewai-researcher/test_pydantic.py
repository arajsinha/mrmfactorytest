from pydantic import BaseModel, Field

class TestModel(BaseModel):
    name: str
    age: int = Field(default=0)

m = TestModel(name="Aryan")
print(m)
